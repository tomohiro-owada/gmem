package gmem

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type GitRepo struct {
	Dir       string
	RemoteURL string
}

type GitError struct {
	Op       string
	Category string
	Output   string
	Err      error
}

func (e *GitError) Error() string {
	return e.Op + " failed: " + e.Output
}

func (g GitRepo) Ensure(ctx context.Context) error {
	if g.Dir == "" {
		return errors.New("git_dir is required")
	}
	if _, err := os.Stat(filepath.Join(g.Dir, ".git")); err == nil {
		if g.RemoteURL != "" {
			_ = g.ensureRemote(ctx)
		}
		return nil
	}
	if g.RemoteURL == "" {
		if err := os.MkdirAll(g.Dir, 0o755); err != nil {
			return err
		}
		_, err := g.runIn(ctx, filepath.Dir(g.Dir), "git", "init", filepath.Base(g.Dir))
		return err
	}
	if err := os.MkdirAll(filepath.Dir(g.Dir), 0o755); err != nil {
		return err
	}
	_, err := g.runIn(ctx, filepath.Dir(g.Dir), "git", "clone", g.RemoteURL, filepath.Base(g.Dir))
	return err
}

func (g GitRepo) PullRebase(ctx context.Context) error {
	if !g.hasUpstream(ctx) {
		return nil
	}
	_, err := g.run(ctx, "git", "pull", "--rebase")
	return err
}

func (g GitRepo) AddCommit(ctx context.Context, path, message string) (string, error) {
	if _, err := g.run(ctx, "git", "add", "--", path); err != nil {
		return "", err
	}
	if _, err := g.run(ctx, "git", "commit", "-m", message); err != nil {
		return "", err
	}
	return g.Head(ctx)
}

func (g GitRepo) Push(ctx context.Context) error {
	if !g.hasUpstream(ctx) {
		_, err := g.run(ctx, "git", "push", "-u", "origin", "main")
		return err
	}
	_, err := g.run(ctx, "git", "push")
	return err
}

func (g GitRepo) Head(ctx context.Context) (string, error) {
	out, err := g.run(ctx, "git", "rev-parse", "HEAD")
	return strings.TrimSpace(out), err
}

func (g GitRepo) CurrentBranch(ctx context.Context) (string, error) {
	out, err := g.run(ctx, "git", "branch", "--show-current")
	return strings.TrimSpace(out), err
}

func (g GitRepo) Dirty(ctx context.Context) (bool, error) {
	out, err := g.run(ctx, "git", "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

func (g GitRepo) Unpushed(ctx context.Context) ([]string, error) {
	out, err := g.run(ctx, "git", "rev-list", "--left-only", "--cherry-pick", "--oneline", "HEAD...@{upstream}")
	if err != nil {
		return nil, nil
	}
	var commits []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if isHexCommitPrefix(fields[0]) {
			commits = append(commits, fields[0])
		}
	}
	return commits, nil
}

func isHexCommitPrefix(s string) bool {
	if len(s) < 7 {
		return false
	}
	for _, r := range s {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			continue
		}
		return false
	}
	return true
}

func (g GitRepo) run(ctx context.Context, name string, args ...string) (string, error) {
	return g.runIn(ctx, g.Dir, name, args...)
}

func (g GitRepo) runIn(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	out := stdout.String()
	if err != nil {
		combined := out + stderr.String()
		return combined, &GitError{Op: name + " " + strings.Join(args, " "), Category: categorizeGitError(combined), Output: combined, Err: err}
	}
	return out, nil
}

func (g GitRepo) ensureRemote(ctx context.Context) error {
	out, err := g.run(ctx, "git", "remote")
	if err != nil {
		return err
	}
	for _, remote := range strings.Split(out, "\n") {
		if strings.TrimSpace(remote) == "origin" {
			_, err := g.run(ctx, "git", "remote", "set-url", "origin", g.RemoteURL)
			return err
		}
	}
	_, err = g.run(ctx, "git", "remote", "add", "origin", g.RemoteURL)
	return err
}

func (g GitRepo) hasUpstream(ctx context.Context) bool {
	_, err := g.run(ctx, "git", "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	return err == nil
}

func categorizeGitError(out string) string {
	lower := strings.ToLower(out)
	switch {
	case strings.Contains(lower, "non-fast-forward"), strings.Contains(lower, "fetch first"), strings.Contains(lower, "remote contains work"), strings.Contains(lower, "rejected"):
		return "remote_ahead"
	case strings.Contains(lower, "permission denied"), strings.Contains(lower, "authentication"), strings.Contains(lower, "could not read from remote"):
		return "auth"
	case strings.Contains(lower, "could not resolve host"), strings.Contains(lower, "network"):
		return "network"
	default:
		return "unknown"
	}
}
