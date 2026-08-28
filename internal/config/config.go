// Package config owns Taskman's small persisted settings: which harness CLI
// and which model to invoke it with. Nothing here touches Docker or git —
// that's internal/repos and internal/work.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// harnessModels is the static catalogue of supported harnesses and the
// models each accepts. Extending to a third harness means adding one entry
// here — no other code should need to change.
var harnessModels = map[string][]string{
	"claude": {
		"claude-sonnet-5",
		"claude-opus-5",
		"claude-haiku-4-5-20251001",
	},
	"opencode": {
		"anthropic/claude-sonnet-5",
		"anthropic/claude-opus-5",
	},
}

// HarnessList returns the harnesses Taskman knows how to invoke, in a
// stable order.
func HarnessList() []string {
	return []string{"claude", "opencode"}
}

// ModelList returns the models valid for a given harness. Returns an error
// if the harness is unknown.
func ModelList(harness string) ([]string, error) {
	models, ok := harnessModels[harness]
	if !ok {
		return nil, fmt.Errorf("config: unknown harness %q (known: %v)", harness, HarnessList())
	}
	out := make([]string, len(models))
	copy(out, models)
	return out, nil
}

// Settings is the persisted selection.
type Settings struct {
	Harness string `json:"harness"`
	Model   string `json:"model"`
}

// Store loads/saves Settings from a JSON file, guarding concurrent access.
type Store struct {
	path string
	mu   sync.Mutex
}

// NewStore opens (without yet reading) the settings store at
// <home>/config.json, applying defaults if the file doesn't exist yet.
func NewStore(home string) (*Store, error) {
	if err := os.MkdirAll(home, 0o755); err != nil {
		return nil, fmt.Errorf("config: create home dir %s: %w", home, err)
	}
	s := &Store{path: filepath.Join(home, "config.json")}
	if _, err := os.Stat(s.path); os.IsNotExist(err) {
		if err := s.write(Settings{Harness: "claude", Model: "claude-sonnet-5"}); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) read() (Settings, error) {
	var out Settings
	b, err := os.ReadFile(s.path)
	if err != nil {
		return out, fmt.Errorf("config: read %s: %w", s.path, err)
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return out, fmt.Errorf("config: parse %s: %w", s.path, err)
	}
	return out, nil
}

func (s *Store) write(v Settings) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("config: marshal settings: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("config: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("config: rename %s -> %s: %w", tmp, s.path, err)
	}
	return nil
}

// Get returns the current settings.
func (s *Store) Get() (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.read()
}

// SetHarness validates and persists the harness choice. If the currently
// selected model isn't valid for the new harness, it resets to that
// harness's first model.
func (s *Store) SetHarness(harness string) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	models, err := ModelList(harness)
	if err != nil {
		return Settings{}, err
	}
	cur, err := s.read()
	if err != nil {
		return Settings{}, err
	}
	cur.Harness = harness
	if !contains(models, cur.Model) {
		cur.Model = models[0]
	}
	if err := s.write(cur); err != nil {
		return Settings{}, err
	}
	return cur, nil
}

// SetModel validates the model against the currently selected harness and
// persists it.
func (s *Store) SetModel(model string) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cur, err := s.read()
	if err != nil {
		return Settings{}, err
	}
	models, err := ModelList(cur.Harness)
	if err != nil {
		return Settings{}, err
	}
	if !contains(models, model) {
		return Settings{}, fmt.Errorf("config: model %q is not valid for harness %q (valid: %v)", model, cur.Harness, models)
	}
	cur.Model = model
	if err := s.write(cur); err != nil {
		return Settings{}, err
	}
	return cur, nil
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}
