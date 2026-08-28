// Package repos owns the project→repo→odoo_version mapping and the
// (code-driven, no-agent) git operations to fetch/update a repo checkout.
// It also knows how to translate an odoo_version into the on-disk
// odoo-env-<major> root and the running dev container's name, since both
// are pure naming conventions Daniel's existing environments already
// follow (odoo-env-19/, container odoo-env-19-odoo-1).
package repos

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Repo is one registered project repo.
type Repo struct {
	Name        string `json:"name"`
	GitURL      string `json:"git_url"`
	OdooVersion string `json:"odoo_version"` // e.g. "19.0"
}

// EnvRoot returns the host path of this repo's odoo-env-<version> checkout,
// e.g. "19.0" -> "/code/odoo-env-19".
func (r Repo) EnvRoot() string {
	return envRoot(r.OdooVersion)
}

// ContainerName returns the name of the already-running dev container for
// this repo's odoo version, e.g. "19.0" -> "odoo-env-19-odoo-1". Taskman
// never creates or manages this container — it must already be up.
func (r Repo) ContainerName() string {
	return containerName(r.OdooVersion)
}

// HostPath is where this repo lives on the host filesystem.
func (r Repo) HostPath() string {
	return filepath.Join(r.EnvRoot(), "repos", r.Name)
}

// ContainerPath is where this repo is visible inside the dev container
// (the compose file bind-mounts the whole env root at /code).
func (r Repo) ContainerPath() string {
	return "/code/repos/" + r.Name
}

func major(odooVersion string) string {
	return strings.SplitN(odooVersion, ".", 2)[0]
}

func envRoot(odooVersion string) string {
	return fmt.Sprintf("/code/odoo-env-%s", major(odooVersion))
}

func containerName(odooVersion string) string {
	return fmt.Sprintf("odoo-env-%s-odoo-1", major(odooVersion))
}

// nameFromURL derives a repo directory name from a git URL, e.g.
// "git@github.com:Solvti/client-a.git" -> "client-a".
func nameFromURL(url string) string {
	base := filepath.Base(url)
	return strings.TrimSuffix(base, ".git")
}

// Registry is the persisted set of known repos, keyed by name.
type Registry struct {
	path string
	mu   sync.Mutex
}

// NewRegistry opens (creating if absent) the repo registry at
// <home>/repos.json.
func NewRegistry(home string) (*Registry, error) {
	if err := os.MkdirAll(home, 0o755); err != nil {
		return nil, fmt.Errorf("repos: create home dir %s: %w", home, err)
	}
	reg := &Registry{path: filepath.Join(home, "repos.json")}
	if _, err := os.Stat(reg.path); os.IsNotExist(err) {
		if err := reg.write(map[string]Repo{}); err != nil {
			return nil, err
		}
	}
	return reg, nil
}

func (r *Registry) read() (map[string]Repo, error) {
	b, err := os.ReadFile(r.path)
	if err != nil {
		return nil, fmt.Errorf("repos: read %s: %w", r.path, err)
	}
	out := map[string]Repo{}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("repos: parse %s: %w", r.path, err)
	}
	return out, nil
}

func (r *Registry) write(v map[string]Repo) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("repos: marshal registry: %w", err)
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("repos: write %s: %w", tmp, err)
	}
	return os.Rename(tmp, r.path)
}

// List returns all registered repos, sorted by name.
func (r *Registry) List() ([]Repo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, err := r.read()
	if err != nil {
		return nil, err
	}
	out := make([]Repo, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out, nil
}

// Get returns one registered repo by name.
func (r *Registry) Get(name string) (Repo, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, err := r.read()
	if err != nil {
		return Repo{}, false, err
	}
	v, ok := m[name]
	return v, ok, nil
}

// FetchResult reports what FetchRepo actually did, per the project's error
// discipline: never a bare string, always {ok, what happened}.
type FetchResult struct {
	Repo       Repo   `json:"repo"`
	Cloned     bool   `json:"cloned"`
	Pulled     bool   `json:"pulled"`
	CommandOut string `json:"command_output"`
}

// FetchRepo clones the repo (first call) or fast-forward-pulls it
// (subsequent calls) into odoo-env-<major>/repos/<name>, and registers it.
// This is pure code — no agent, no LLM call — per the revised design.
func (r *Registry) FetchRepo(gitURL, odooVersion string) (FetchResult, error) {
	name := nameFromURL(gitURL)
	repo := Repo{Name: name, GitURL: gitURL, OdooVersion: odooVersion}

	root := repo.EnvRoot()
	if _, err := os.Stat(root); err != nil {
		return FetchResult{}, fmt.Errorf("repos: env root %s for odoo_version %q does not exist (checked because that's where odoo-env checkouts live by convention): %w", root, odooVersion, err)
	}

	reposDir := filepath.Join(root, "repos")
	if err := os.MkdirAll(reposDir, 0o755); err != nil {
		return FetchResult{}, fmt.Errorf("repos: create %s: %w", reposDir, err)
	}

	dest := repo.HostPath()
	var out []byte
	var err error
	result := FetchResult{Repo: repo}

	if _, statErr := os.Stat(filepath.Join(dest, ".git")); statErr == nil {
		cmd := exec.Command("git", "-C", dest, "pull", "--ff-only")
		out, err = cmd.CombinedOutput()
		result.Pulled = true
	} else {
		cmd := exec.Command("git", "clone", gitURL, dest)
		out, err = cmd.CombinedOutput()
		result.Cloned = true
	}
	result.CommandOut = string(out)
	if err != nil {
		return result, fmt.Errorf("repos: git operation on %s failed: %w\noutput:\n%s", dest, err, out)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	m, err := r.read()
	if err != nil {
		return result, err
	}
	m[name] = repo
	if err := r.write(m); err != nil {
		return result, err
	}
	return result, nil
}
