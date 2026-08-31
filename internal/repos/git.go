// git.go holds the plain, code-driven git/PR operations the implement flow
// needs: pull main, pull the shared odoo/enterprise checkouts, create a
// task branch, and — once the agent's done — push it and open a PR. All of
// it shells out to `git`/`gh`; none of it is agent-authored.
package repos

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// GitResult is the project's error-discipline shape applied to git/gh
// calls: never a bare string, always {ok, what ran, what came back}.
type GitResult struct {
	Command string `json:"command"`
	Ok      bool   `json:"ok"`
	Output  string `json:"output"`
}

func runIn(dir string, name string, args ...string) GitResult {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return GitResult{
		Command: name + " " + fmt.Sprintf("%v", args),
		Ok:      err == nil,
		Output:  string(out),
	}
}

// DefaultBranch returns the repo's default branch, trying "main" then
// "master" (checked as a remote-tracking ref so it works even if the
// working tree currently has some other branch checked out).
func DefaultBranch(repoPath string) (string, error) {
	for _, candidate := range []string{"main", "master"} {
		cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", "origin/"+candidate)
		cmd.Dir = repoPath
		if err := cmd.Run(); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("repos: neither origin/main nor origin/master exists in %s", repoPath)
}

// PullMainBranch checks out the repo's default branch and fast-forward
// pulls it. Refuses (returns Ok:false, never force-discards) if the
// working tree has local modifications, surfacing that loudly per the
// spec's "never auto-discard" rule.
func PullMainBranch(repoPath string) (branch string, checkout, pull GitResult, err error) {
	branch, err = DefaultBranch(repoPath)
	if err != nil {
		return "", GitResult{}, GitResult{}, err
	}

	status := runIn(repoPath, "git", "status", "--porcelain")
	if status.Ok && status.Output != "" {
		return branch, GitResult{Command: status.Command, Ok: false, Output: "working tree is dirty, refusing to switch/pull:\n" + status.Output}, GitResult{}, nil
	}

	checkout = runIn(repoPath, "git", "checkout", branch)
	if !checkout.Ok {
		return branch, checkout, GitResult{}, nil
	}
	pull = runIn(repoPath, "git", "pull", "--ff-only")
	return branch, checkout, pull, nil
}

// PullUpstream fast-forward-pulls the shared odoo/ and enterprise/
// checkouts under the version root, skipping (not failing) any that
// aren't present — some odoo-env setups may lack enterprise, per §3.1.
func PullUpstream(envRoot string) map[string]GitResult {
	results := map[string]GitResult{}
	for _, name := range []string{"odoo", "enterprise"} {
		dir := filepath.Join(envRoot, name)
		if _, err := os.Stat(dir); err != nil {
			continue // not present in this env root — not an error
		}
		status := runIn(dir, "git", "status", "--porcelain")
		if status.Ok && status.Output != "" {
			results[name] = GitResult{Command: status.Command, Ok: false, Output: "dirty, refusing to pull:\n" + status.Output}
			continue
		}
		results[name] = runIn(dir, "git", "pull", "--ff-only")
	}
	return results
}

// CreateTaskBranch creates (or, if it already exists, checks out) the
// given branch name from the currently-checked-out HEAD.
func CreateTaskBranch(repoPath, branch string) GitResult {
	res := runIn(repoPath, "git", "checkout", "-b", branch)
	if res.Ok {
		return res
	}
	// Already exists (e.g. a re-run of Implement) — check it out instead of
	// failing outright.
	return runIn(repoPath, "git", "checkout", branch)
}

// HasCommitsSince reports whether branch (the currently checked-out task
// branch) has any commits base doesn't — i.e. whether the agent actually
// changed anything. Used to short-circuit push/PR when it didn't: an
// agent that hit a tool-permission wall or otherwise made no edits still
// exits 0, and without this check runImplement would push a branch
// identical to base and then attempt a pointless PR against it.
func HasCommitsSince(repoPath, base, branch string) (bool, error) {
	cmd := exec.Command("git", "rev-list", "--count", base+".."+branch)
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("repos: rev-list %s..%s in %s: %w", base, branch, repoPath, err)
	}
	var count int
	if _, err := fmt.Sscanf(string(out), "%d", &count); err != nil {
		return false, fmt.Errorf("repos: parse rev-list output %q: %w", out, err)
	}
	return count > 0, nil
}

// PushBranch pushes the branch to origin, setting upstream.
func PushBranch(repoPath, branch string) GitResult {
	return runIn(repoPath, "git", "push", "-u", "origin", branch)
}

// OpenPR opens a draft PR via the `gh` CLI. If `gh` isn't installed or
// isn't authenticated, this returns Ok:false with the gh error text rather
// than a Go error — that's an expected, loggable outcome in an environment
// with no GitHub credentials configured yet, not a bug.
func OpenPR(repoPath, branch, base, title, body string) GitResult {
	return runIn(repoPath, "gh", "pr", "create",
		"--base", base,
		"--head", branch,
		"--title", title,
		"--body", body,
		"--draft",
	)
}
