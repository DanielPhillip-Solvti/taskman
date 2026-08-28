// Package work runs tasks: it builds a prompt, docker-execs the configured
// harness CLI inside the repo's already-running dev container, and tracks
// the resulting process so its output can be polled and it can be
// interrupted. No package here decides *what* the agent should do — that's
// the prompt template below — it only decides *that* and *where* it runs.
package work

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/solvti/taskman/internal/config"
	"github.com/solvti/taskman/internal/repos"
)

// Kind distinguishes the two phases the brief's state machine actually
// needs code-driven support for today: refine and implement. (Complete /
// evidence-capture can be added the same way later without changing this
// shape.)
type Kind string

const (
	KindRefine    Kind = "refine"
	KindImplement Kind = "implement"
)

// Status is a task's lifecycle state.
type Status string

const (
	StatusQueued      Status = "queued"
	StatusRunning     Status = "running"
	StatusDone        Status = "done"
	StatusFailed      Status = "failed"
	StatusInterrupted Status = "interrupted"
)

const (
	refinementTemplate = `You are refining a support ticket for the "%s" repo, task #%d.

Ticket description as written by the reporter:
---
%s
---

Investigate the current codebase to understand what this request actually
requires. Produce:
1. A short refined specification (what will change, and where).
2. Acceptance criteria as a bullet list.
3. Any open questions that need a human answer before implementation.

Print your findings as plain markdown. Do not modify any files.`

	implementTemplate = `You are implementing a support ticket for the "%s" repo, task #%d.

Ticket description:
---
%s
---

Make the code change on the current branch. Commit your work locally with a
clear commit message as you go. When done, summarize what changed and which
files were touched.`
)

// Task is one queued/running/finished agent invocation.
type Task struct {
	Number   int    `json:"number"`
	RepoName string `json:"repo_name"`
	Kind     Kind   `json:"kind"`
	Status   Status `json:"status"`
	LogPath  string `json:"-"`
	Error    string `json:"error,omitempty"`

	mu  sync.Mutex
	cmd *exec.Cmd
}

// Manager holds all tasks in memory (log content lives on disk) and
// enforces one active task per repo, per the concurrency rule that already
// proved out well in the original design.
type Manager struct {
	home     string
	cfg      *config.Store
	regis    *repos.Registry
	mu       sync.Mutex
	tasks    map[int]*Task
	repoBusy map[string]int // repo name -> task number currently holding it
}

// NewManager wires a Manager against the given config store and repo
// registry, storing task logs under <home>/tasks/.
func NewManager(home string, cfg *config.Store, regis *repos.Registry) (*Manager, error) {
	logsDir := filepath.Join(home, "tasks")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		return nil, fmt.Errorf("work: create tasks dir %s: %w", logsDir, err)
	}
	return &Manager{
		home:     home,
		cfg:      cfg,
		regis:    regis,
		tasks:    map[int]*Task{},
		repoBusy: map[string]int{},
	}, nil
}

// ErrRepoBusy is returned when a repo already has an active task.
type ErrRepoBusy struct {
	RepoName   string
	HolderTask int
}

func (e ErrRepoBusy) Error() string {
	return fmt.Sprintf("work: repo %q is busy with task #%d", e.RepoName, e.HolderTask)
}

// ErrUnknownRepo is returned when the named repo hasn't been registered via
// FetchRepo yet — a client error, not a server error.
type ErrUnknownRepo struct {
	RepoName string
}

func (e ErrUnknownRepo) Error() string {
	return fmt.Sprintf("work: repo %q is not registered (call FetchRepo first)", e.RepoName)
}

// QueueTaskRefinement starts a refinement-phase agent run.
func (m *Manager) QueueTaskRefinement(number int, repoName, description string) (*Task, error) {
	return m.queue(number, repoName, description, KindRefine, refinementTemplate)
}

// QueueTask starts an implementation-phase agent run.
func (m *Manager) QueueTask(number int, repoName, description string) (*Task, error) {
	return m.queue(number, repoName, description, KindImplement, implementTemplate)
}

func (m *Manager) queue(number int, repoName, description string, kind Kind, template string) (*Task, error) {
	repo, ok, err := m.regis.Get(repoName)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrUnknownRepo{RepoName: repoName}
	}

	m.mu.Lock()
	if holder, busy := m.repoBusy[repoName]; busy {
		m.mu.Unlock()
		return nil, ErrRepoBusy{RepoName: repoName, HolderTask: holder}
	}
	m.repoBusy[repoName] = number
	m.mu.Unlock()

	settings, err := m.cfg.Get()
	if err != nil {
		m.releaseRepo(repoName)
		return nil, err
	}

	logPath := filepath.Join(m.home, "tasks", fmt.Sprintf("%d.log", number))
	task := &Task{Number: number, RepoName: repoName, Kind: kind, Status: StatusQueued, LogPath: logPath}

	m.mu.Lock()
	m.tasks[number] = task
	m.mu.Unlock()

	prompt := fmt.Sprintf(template, repoName, number, description)
	go m.run(task, repo, settings, prompt)

	return task, nil
}

func (m *Manager) releaseRepo(repoName string) {
	m.mu.Lock()
	delete(m.repoBusy, repoName)
	m.mu.Unlock()
}

func harnessArgs(harness, model, prompt string) (string, []string, error) {
	switch harness {
	case "claude":
		return "claude", []string{"-p", prompt, "--model", model}, nil
	case "opencode":
		return "opencode", []string{"run", prompt, "--model", model}, nil
	default:
		return "", nil, fmt.Errorf("work: unknown harness %q", harness)
	}
}

func (m *Manager) run(task *Task, repo repos.Repo, settings config.Settings, prompt string) {
	defer m.releaseRepo(repo.Name)

	logFile, err := os.Create(task.LogPath)
	if err != nil {
		m.fail(task, fmt.Errorf("work: create log file %s: %w", task.LogPath, err))
		return
	}
	defer logFile.Close()
	w := bufio.NewWriter(logFile)
	defer w.Flush()

	bin, args, err := harnessArgs(settings.Harness, settings.Model, prompt)
	if err != nil {
		m.fail(task, err)
		return
	}

	dockerArgs := append([]string{"exec", "-w", repo.ContainerPath(), repo.ContainerName(), bin}, args...)
	cmd := exec.Command("docker", dockerArgs...)
	cmd.Stdout = w
	cmd.Stderr = w

	task.mu.Lock()
	task.cmd = cmd
	task.Status = StatusRunning
	task.mu.Unlock()

	fmt.Fprintf(w, "--- task #%d (%s) starting: docker %v ---\n", task.Number, task.Kind, dockerArgs)
	w.Flush()

	err = cmd.Run()

	task.mu.Lock()
	defer task.mu.Unlock()
	if task.Status == StatusInterrupted {
		fmt.Fprintf(w, "--- task #%d interrupted ---\n", task.Number)
		return
	}
	if err != nil {
		task.Status = StatusFailed
		task.Error = err.Error()
		fmt.Fprintf(w, "--- task #%d failed: %v ---\n", task.Number, err)
		return
	}
	task.Status = StatusDone
	fmt.Fprintf(w, "--- task #%d done ---\n", task.Number)
}

func (m *Manager) fail(task *Task, err error) {
	task.mu.Lock()
	task.Status = StatusFailed
	task.Error = err.Error()
	task.mu.Unlock()
	m.releaseRepo(task.RepoName)
}

// Output is what GetTaskOutput hands back to a poller.
type Output struct {
	Number int    `json:"number"`
	Status Status `json:"status"`
	Error  string `json:"error,omitempty"`
	Log    string `json:"log"`
}

// GetTaskOutput returns the task's current status and its full log so far.
func (m *Manager) GetTaskOutput(number int) (Output, error) {
	m.mu.Lock()
	task, ok := m.tasks[number]
	m.mu.Unlock()
	if !ok {
		return Output{}, fmt.Errorf("work: no such task #%d", number)
	}

	task.mu.Lock()
	status, taskErr := task.Status, task.Error
	task.mu.Unlock()

	logBytes, err := os.ReadFile(task.LogPath)
	if err != nil {
		return Output{}, fmt.Errorf("work: read log %s for task #%d: %w", task.LogPath, number, err)
	}
	return Output{Number: number, Status: status, Error: taskErr, Log: string(logBytes)}, nil
}

// InterruptTask sends SIGTERM to a running task's process, escalating to
// SIGKILL after 10s if it hasn't exited, matching the stop semantics from
// the original design.
func (m *Manager) InterruptTask(number int) error {
	m.mu.Lock()
	task, ok := m.tasks[number]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("work: no such task #%d", number)
	}

	task.mu.Lock()
	cmd := task.cmd
	status := task.Status
	if status == StatusRunning {
		task.Status = StatusInterrupted
	}
	task.mu.Unlock()

	if status != StatusRunning || cmd == nil || cmd.Process == nil {
		return fmt.Errorf("work: task #%d is not running (status=%s)", number, status)
	}

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("work: SIGTERM task #%d (pid %d): %w", number, cmd.Process.Pid, err)
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		done := make(chan struct{})
		go func() { cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-ctx.Done():
			_ = cmd.Process.Kill()
		}
	}()
	return nil
}
