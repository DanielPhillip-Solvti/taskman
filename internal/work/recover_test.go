package work

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/DanielPhillip-Solvti/taskman/internal/config"
	"github.com/DanielPhillip-Solvti/taskman/internal/repos"
)

// newRecoveryManager writes the <number>.json/<number>.log pair a real run
// would have left behind, as if from a previous process, then constructs a
// fresh Manager against that same home dir to exercise loadTasks.
func newRecoveryManager(t *testing.T, meta taskMeta, log string) *Manager {
	t.Helper()
	home := t.TempDir()
	tasksDir := filepath.Join(home, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}

	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	n := strconv.Itoa(meta.Number)
	if err := os.WriteFile(filepath.Join(tasksDir, n+".json"), data, 0o644); err != nil {
		t.Fatalf("write meta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tasksDir, n+".log"), []byte(log), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	cfg, err := config.NewStore(home)
	if err != nil {
		t.Fatalf("config.NewStore: %v", err)
	}
	regis, err := repos.NewRegistry(home)
	if err != nil {
		t.Fatalf("repos.NewRegistry: %v", err)
	}
	m, err := NewManager(home, cfg, regis)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m
}

func TestNewManagerRecoversFinishedTask(t *testing.T) {
	m := newRecoveryManager(t, taskMeta{
		Number: 4821, RepoName: "demo", Kind: KindImplement, Status: StatusDone,
		Branch: "task/4821-reset-environments", PRURL: "https://example.com/pr/1",
	}, "agent log content\n")

	out, err := m.GetTaskOutput(4821)
	if err != nil {
		t.Fatalf("GetTaskOutput after restart: %v", err)
	}
	if out.Status != StatusDone {
		t.Errorf("status = %q, want %q", out.Status, StatusDone)
	}
	if out.Branch != "task/4821-reset-environments" {
		t.Errorf("branch = %q, want task/4821-reset-environments", out.Branch)
	}
	if out.PRURL != "https://example.com/pr/1" {
		t.Errorf("pr_url = %q", out.PRURL)
	}
	if out.Log != "agent log content\n" {
		t.Errorf("log = %q", out.Log)
	}
}

func TestNewManagerFailsOrphanedRunningTask(t *testing.T) {
	m := newRecoveryManager(t, taskMeta{
		Number: 100, RepoName: "demo", Kind: KindRefine, Status: StatusRunning,
	}, "in progress...\n")

	out, err := m.GetTaskOutput(100)
	if err != nil {
		t.Fatalf("GetTaskOutput after restart: %v", err)
	}
	if out.Status != StatusFailed {
		t.Errorf("status = %q, want %q (orphaned by restart)", out.Status, StatusFailed)
	}
	if out.Error == "" {
		t.Error("expected an explanatory error for the orphaned task")
	}

	// Nothing can ever finish or interrupt an orphaned task, so its repo
	// must not still be considered busy after recovery — otherwise it'd be
	// stuck forever.
	m.mu.Lock()
	_, busy := m.repoBusy["demo"]
	m.mu.Unlock()
	if busy {
		t.Error("repo left busy after recovering an orphaned running task")
	}

	// The recovered failure should also have been persisted back to disk,
	// not just held in memory, so a second restart doesn't flip it back to
	// "running".
	metaPath := m.metaPath(100)
	data, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("read persisted metadata: %v", err)
	}
	var meta taskMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("parse persisted metadata: %v", err)
	}
	if meta.Status != StatusFailed {
		t.Errorf("persisted status = %q, want %q", meta.Status, StatusFailed)
	}
}
