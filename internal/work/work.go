// Package work runs tasks: it builds a prompt, docker-execs the configured
// harness CLI inside the repo's already-running dev container, and tracks
// the resulting process so its output can be polled and it can be
// interrupted. No package here decides *what* the agent should do — that's
// the prompt template below — it only decides *that* and *where* it runs.
package work

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
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
You are already on the task's dedicated branch and %s and %s have already
been updated to their latest upstream — do not switch branches yourself.

Ticket description:
---
%s
---

Make the code change on this branch. Commit your work locally with a clear
commit message as you go. When done, summarize what changed and which files
were touched.`

	summaryTemplate = `You just finished implementing task #%d on the current branch.

Write a concise pull-request summary of the changes you made: a short
paragraph describing what changed and why, followed by a bullet list of the
key changes (files/areas touched). Base it on the actual commits/diff on
this branch, not on the original ticket text. Print only the summary —
no preamble, no "Sure, here's a summary".`
)

// Task is one queued/running/finished agent invocation.
type Task struct {
	Number      int    `json:"number"`
	RepoName    string `json:"repo_name"`
	Kind        Kind   `json:"kind"`
	Status      Status `json:"status"`
	Branch      string `json:"branch,omitempty"`
	PRURL       string `json:"pr_url,omitempty"`
	Summary     string `json:"summary,omitempty"`
	LogPath     string `json:"-"`
	SummaryPath string `json:"-"`
	Error       string `json:"error,omitempty"`

	mu            sync.Mutex
	cmd           *exec.Cmd
	containerName string // set while an agent step is running, for InterruptTask
	harnessBin    string
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

// writeSummary persists the agent's PR summary to its own file, separate
// from the raw <number>.log transcript, so a later completion/write-back
// step can reuse just the summary without reparsing the whole log.
func (m *Manager) writeSummary(number int, text string) (string, error) {
	path := filepath.Join(m.home, "tasks", fmt.Sprintf("%d.summary.md", number))
	if err := os.WriteFile(path, []byte(text+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("work: write summary file %s: %w", path, err)
	}
	return path, nil
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

// QueueTaskRefinement starts a refinement-phase agent run. Refinement is
// read-only investigation — it never touches git.
func (m *Manager) QueueTaskRefinement(number int, repoName, title, description string) (*Task, error) {
	return m.queue(number, repoName, title, description, KindRefine)
}

// QueueTask starts an implementation-phase run. Per the revised flow, this
// is more than just invoking the agent: the daemon itself (never the
// agent) pulls the repo's default branch, pulls the shared odoo/enterprise
// checkouts, and creates the task's dedicated branch *before* delegating
// the actual change to the agent — then, once the agent finishes
// successfully, pushes that branch and opens a draft PR. See runImplement.
func (m *Manager) QueueTask(number int, repoName, title, description string) (*Task, error) {
	return m.queue(number, repoName, title, description, KindImplement)
}

func (m *Manager) queue(number int, repoName, title, description string, kind Kind) (*Task, error) {
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

	switch kind {
	case KindRefine:
		go m.runRefine(task, repo, settings, description)
	case KindImplement:
		go m.runImplement(task, repo, settings, title, description)
	}

	return task, nil
}

// taskBranch derives a git branch name from the task number and title,
// e.g. (4821, "Reset Environments") -> "task/4821-reset-environments". The
// daemon owns branch naming — never the agent — so it stays predictable
// and greppable.
var slugNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func taskBranch(number int, title string) string {
	slug := strings.ToLower(strings.TrimSpace(title))
	slug = slugNonAlnum.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		return fmt.Sprintf("task/%d", number)
	}
	if len(slug) > 50 {
		slug = strings.Trim(slug[:50], "-")
	}
	return fmt.Sprintf("task/%d-%s", number, slug)
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

// runRefine is the simple case: no git orchestration, just a read-only
// agent investigation.
func (m *Manager) runRefine(task *Task, repo repos.Repo, settings config.Settings, description string) {
	defer m.releaseRepo(repo.Name)

	logFile, err := os.Create(task.LogPath)
	if err != nil {
		m.fail(task, fmt.Errorf("work: create log file %s: %w", task.LogPath, err))
		return
	}
	defer logFile.Close()
	w := bufio.NewWriter(logFile)
	defer w.Flush()

	prompt := fmt.Sprintf(refinementTemplate, repo.Name, task.Number, description)
	if ok := m.runAgent(task, repo, settings, w, prompt); ok {
		m.finish(task, w, StatusDone)
	}
}

// runImplement is the revised flow: the daemon itself — never the agent —
// pulls the repo's default branch, pulls the shared odoo/enterprise
// checkouts, and creates the task's dedicated branch; only then is the
// agent delegated the actual code change; once it succeeds, the daemon
// pushes the branch and opens a draft PR. Each step is logged so a stuck
// or failed run is diagnosable from the same transcript the agent's output
// lands in.
func (m *Manager) runImplement(task *Task, repo repos.Repo, settings config.Settings, title, description string) {
	defer m.releaseRepo(repo.Name)

	logFile, err := os.Create(task.LogPath)
	if err != nil {
		m.fail(task, fmt.Errorf("work: create log file %s: %w", task.LogPath, err))
		return
	}
	defer logFile.Close()
	w := bufio.NewWriter(logFile)
	defer w.Flush()

	step := func(format string, args ...any) {
		fmt.Fprintf(w, "--- task #%d: "+format+" ---\n", append([]any{task.Number}, args...)...)
		w.Flush()
	}

	step("pulling %s's default branch", repo.Name)
	baseBranch, checkoutRes, pullRes, err := repos.PullMainBranch(repo.HostPath())
	if err != nil {
		m.fail(task, fmt.Errorf("work: determine default branch for %s: %w", repo.Name, err))
		fmt.Fprintf(w, "%v\n", err)
		return
	}
	fmt.Fprintf(w, "checkout %s: ok=%v\n%s", baseBranch, checkoutRes.Ok, checkoutRes.Output)
	if !checkoutRes.Ok {
		m.fail(task, fmt.Errorf("work: checkout %s in %s failed: %s", baseBranch, repo.Name, checkoutRes.Output))
		return
	}
	fmt.Fprintf(w, "pull: ok=%v\n%s", pullRes.Ok, pullRes.Output)
	if !pullRes.Ok {
		m.fail(task, fmt.Errorf("work: pull %s in %s failed (see log)", baseBranch, repo.Name))
		return
	}

	step("pulling shared odoo/enterprise checkouts in %s", repo.EnvRoot())
	upstream := repos.PullUpstream(repo.EnvRoot())
	for name, res := range upstream {
		fmt.Fprintf(w, "%s: ok=%v\n%s", name, res.Ok, res.Output)
		if !res.Ok {
			m.fail(task, fmt.Errorf("work: pull upstream %q failed (see log) — aborting rather than building against a dirty/stale checkout", name))
			return
		}
	}

	branch := taskBranch(task.Number, title)
	task.mu.Lock()
	task.Branch = branch
	task.mu.Unlock()
	step("creating task branch %s", branch)
	branchRes := repos.CreateTaskBranch(repo.HostPath(), branch)
	fmt.Fprintf(w, "ok=%v\n%s", branchRes.Ok, branchRes.Output)
	if !branchRes.Ok {
		m.fail(task, fmt.Errorf("work: create branch %s in %s failed (see log)", branch, repo.Name))
		return
	}
	w.Flush()

	step("delegating implementation to agent")
	prompt := fmt.Sprintf(implementTemplate, repo.Name, task.Number, "odoo", "enterprise", description)
	if ok := m.runAgent(task, repo, settings, w, prompt); !ok {
		return // runAgent already set a terminal status (failed/interrupted) and logged why
	}

	step("agent finished — asking it to summarize its changes for the PR")
	summaryPrompt := fmt.Sprintf(summaryTemplate, task.Number)
	summary, summaryOK := m.runAgentCapture(task, repo, settings, w, summaryPrompt)
	summary = strings.TrimSpace(summary)
	if !summaryOK {
		return // interrupted/failed mid-summary; runAgentCapture already set the terminal status
	}
	if summary == "" {
		// Not fatal — a blank summary just means the PR body falls back to
		// the raw ticket description below.
		fmt.Fprintf(w, "(agent returned no summary text; falling back to ticket description for the PR body)\n")
	} else {
		summaryPath, err := m.writeSummary(task.Number, summary)
		if err != nil {
			// Non-fatal: the summary is already in the task log and in
			// memory (task.Summary) via the capture above, this is just
			// the convenience copy for later reuse (e.g. chatter write-back).
			fmt.Fprintf(w, "(could not write summary file: %v)\n", err)
		} else {
			task.mu.Lock()
			task.SummaryPath = summaryPath
			task.mu.Unlock()
			fmt.Fprintf(w, "summary written to %s\n", summaryPath)
		}
		task.mu.Lock()
		task.Summary = summary
		task.mu.Unlock()
	}

	step("pushing %s", branch)
	pushRes := repos.PushBranch(repo.HostPath(), branch)
	fmt.Fprintf(w, "ok=%v\n%s", pushRes.Ok, pushRes.Output)
	if !pushRes.Ok {
		// The implementation itself succeeded; only the push/PR step
		// failed (e.g. no remote credentials in this environment). Report
		// that plainly but don't mark the whole task failed — the commits
		// are real and sitting on a real local branch, which is the part
		// that matters most.
		task.mu.Lock()
		task.Error = "push failed (see log) — implementation is committed locally on branch " + branch
		task.mu.Unlock()
		m.finish(task, w, StatusDone)
		return
	}

	step("opening draft PR against %s", baseBranch)
	prTitle := fmt.Sprintf("[task #%d] %s", task.Number, title)
	prBody := fmt.Sprintf("Automated implementation for task #%d.\n\n%s", task.Number, description)
	if summary != "" {
		prBody = fmt.Sprintf("Automated implementation for task #%d.\n\n%s", task.Number, summary)
	}
	prRes := repos.OpenPR(repo.HostPath(), branch, baseBranch, prTitle, prBody)
	fmt.Fprintf(w, "ok=%v\n%s", prRes.Ok, prRes.Output)
	if !prRes.Ok {
		task.mu.Lock()
		task.Error = "PR creation failed (see log) — branch " + branch + " is pushed, open the PR manually"
		task.mu.Unlock()
	} else if url := strings.TrimSpace(prRes.Output); url != "" {
		task.mu.Lock()
		task.PRURL = url
		task.mu.Unlock()
	}

	m.finish(task, w, StatusDone)
}

// runAgent docker-execs the harness CLI with the given prompt and blocks
// until it exits, tracking the process on task.cmd so InterruptTask can
// reach it. Returns true if the agent completed successfully; on false, it
// has already set task.Status to failed/interrupted and logged why, so the
// caller should simply stop.
func (m *Manager) runAgent(task *Task, repo repos.Repo, settings config.Settings, w *bufio.Writer, prompt string) bool {
	ok, _ := m.runAgentInto(task, repo, settings, w, prompt, nil)
	return ok
}

// runAgentCapture behaves like runAgent but also returns the agent's
// combined stdout as a string (e.g. for the PR-summary step) — the full
// output still lands in the task log either way.
func (m *Manager) runAgentCapture(task *Task, repo repos.Repo, settings config.Settings, w *bufio.Writer, prompt string) (string, bool) {
	var captured bytes.Buffer
	ok, _ := m.runAgentInto(task, repo, settings, w, prompt, &captured)
	return captured.String(), ok
}

// runAgentInto is the shared implementation: it docker-execs the harness
// CLI, always writing its stdout/stderr to the task log (w), and — if
// capture is non-nil — duplicating stdout into it as well.
func (m *Manager) runAgentInto(task *Task, repo repos.Repo, settings config.Settings, w *bufio.Writer, prompt string, capture *bytes.Buffer) (bool, error) {
	bin, args, err := harnessArgs(settings.Harness, settings.Model, prompt)
	if err != nil {
		m.fail(task, err)
		return false, err
	}

	dockerArgs := append([]string{"exec", "-w", repo.ContainerPath(), repo.ContainerName(), bin}, args...)
	cmd := exec.Command("docker", dockerArgs...)
	if capture != nil {
		cmd.Stdout = io.MultiWriter(w, capture)
	} else {
		cmd.Stdout = w
	}
	cmd.Stderr = w

	task.mu.Lock()
	task.cmd = cmd
	task.containerName = repo.ContainerName()
	task.harnessBin = bin
	task.Status = StatusRunning
	task.mu.Unlock()

	fmt.Fprintf(w, "--- task #%d (%s) agent starting: docker %v ---\n", task.Number, task.Kind, dockerArgs)
	w.Flush()

	err = cmd.Run()

	task.mu.Lock()
	defer task.mu.Unlock()
	if task.Status == StatusInterrupted {
		fmt.Fprintf(w, "--- task #%d interrupted ---\n", task.Number)
		return false, err
	}
	if err != nil {
		task.Status = StatusFailed
		task.Error = err.Error()
		fmt.Fprintf(w, "--- task #%d failed: %v ---\n", task.Number, err)
		return false, err
	}
	return true, nil
}

// finish marks a task done and writes the closing log line. Kept separate
// from runAgent because runImplement has more steps to run after the
// agent succeeds.
func (m *Manager) finish(task *Task, w *bufio.Writer, status Status) {
	task.mu.Lock()
	task.Status = status
	task.mu.Unlock()
	fmt.Fprintf(w, "--- task #%d %s ---\n", task.Number, status)
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
	Number  int    `json:"number"`
	Status  Status `json:"status"`
	Branch  string `json:"branch,omitempty"`
	PRURL   string `json:"pr_url,omitempty"`
	Summary string `json:"summary,omitempty"`
	Error   string `json:"error,omitempty"`
	Log     string `json:"log"`
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
	status, taskErr, branch, prURL, summary := task.Status, task.Error, task.Branch, task.PRURL, task.Summary
	task.mu.Unlock()

	logBytes, err := os.ReadFile(task.LogPath)
	if err != nil {
		return Output{}, fmt.Errorf("work: read log %s for task #%d: %w", task.LogPath, number, err)
	}
	return Output{Number: number, Status: status, Branch: branch, PRURL: prURL, Summary: summary, Error: taskErr, Log: string(logBytes)}, nil
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
	containerName := task.containerName
	harnessBin := task.harnessBin
	status := task.Status
	if status == StatusRunning {
		task.Status = StatusInterrupted
	}
	task.mu.Unlock()

	if status != StatusRunning || cmd == nil || cmd.Process == nil {
		return fmt.Errorf("work: task #%d is not running (status=%s)", number, status)
	}

	// Signaling the local `docker exec` client process does NOT propagate
	// into the container — docker exec doesn't forward host signals to the
	// exec'd process. The only reliable way to stop the actual agent
	// process is to reach into the container itself and signal it there by
	// binary name, then fall back to killing the local docker CLI process
	// (which at least drops our side of the connection) if that somehow
	// doesn't unblock cmd.Wait().
	pkill := func(sig string) {
		_ = exec.Command("docker", "exec", containerName, "pkill", sig, "-f", harnessBin).Run()
	}
	pkill("-TERM")
	_ = cmd.Process.Signal(syscall.SIGTERM) // best-effort, likely a no-op against the docker CLI client

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		done := make(chan struct{})
		go func() { cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-ctx.Done():
			pkill("-KILL")
			_ = cmd.Process.Kill()
		}
	}()
	return nil
}
