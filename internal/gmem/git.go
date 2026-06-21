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
	_, err := g.run(ctx, "git", "push")
	return err
}

func (g GitRepo) Head(ctx context.Context) (string, error) {
	out, err := g.run(ctx, "git", "rev-parse", "HEAD")
	return strings.TrimSpace(out), err
}

func (g GitRepo) Unpushed(ctx context.Context) ([]string, error) {
	out, err := g.run(ctx, "git", "rev-list", "--left-only", "--cherry-pick", "--oneline", "HEAD...@{upstream}")
	if err != nil {
		return nil, nil
	}
	var commits []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(line) != "" {
			commits = append(commits, strings.Fields(line)[0])
		}
	}
	return commits, nil
}

func (g GitRepo) run(ctx context.Context, name string, args ...string) (string, error) {
	return g.runIn(ctx, g.Dir, name, args...)
}

func (g GitRepo) runIn(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := buf.String()
	if err != nil {
		return out, &GitError{Op: name + " " + strings.Join(args, " "), Category: categorizeGitError(out), Output: out, Err: err}
	}
	return out, nil
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
