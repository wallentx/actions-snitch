// Package git contains bounded subprocess and Git/PR orchestration boundaries.
package git

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Runner interface {
	Run(context.Context, string, string, ...string) (string, error)
}
type Commands struct{}

type boundedBuffer struct{ bytes.Buffer }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 8<<20 {
		return 0, errors.New("subprocess output exceeds limit")
	}
	return b.Buffer.Write(p)
}

func (Commands) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	var cmd *exec.Cmd
	switch name {
	case "git":
		// #nosec G204 -- The executable is fixed; callers pass argument arrays with path delimiters and never invoke a shell.
		cmd = exec.CommandContext(ctx, "git", args...)
	case "gh":
		// #nosec G204 -- The executable is fixed; API credentials never appear in these arguments and no shell is used.
		cmd = exec.CommandContext(ctx, "gh", args...)
	default:
		return "", fmt.Errorf("unsupported subprocess %q", name)
	}
	cmd.Dir = dir
	cmd.WaitDelay = 2 * time.Second
	var stdout, stderr boundedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s failed: %w: %s", name, err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimRight(stdout.String(), "\r\n"), nil
}

type Service struct {
	Dir    string
	Runner Runner
	Output io.Writer
}
type Target struct {
	URL    string
	Base   string
	Branch string
}

func (s Service) run(ctx context.Context, name string, args ...string) (string, error) {
	return s.Runner.Run(ctx, s.Dir, name, args...)
}
func (s Service) Clean(ctx context.Context) error {
	status, err := s.run(ctx, "git", "status", "--porcelain")
	if err != nil {
		return err
	}
	if status != "" {
		return errors.New("operation requires a clean worktree")
	}
	return nil
}
func (s Service) Branch(ctx context.Context) (string, error) {
	branch, err := s.run(ctx, "git", "branch", "--show-current")
	if err != nil {
		return "", err
	}
	if branch == "" {
		return "", errors.New("actions-snitch needs a named git branch for branch or PR operations")
	}
	return branch, nil
}

// Prepare checks the PR destination before changing branches or workflow files.
func (s Service) Prepare(ctx context.Context, pr bool, branch string) (Target, error) {
	var target Target
	if pr {
		if err := s.Clean(ctx); err != nil {
			return target, fmt.Errorf("-p: %w", err)
		}
		pushURL, err := s.run(ctx, "git", "remote", "get-url", "--push", "origin")
		if err != nil || pushURL == "" {
			return target, errors.New("-p requires an origin push URL to select the PR repository")
		}
		metadata, err := s.run(ctx, "gh", "repo", "view", pushURL, "--json", "url,defaultBranchRef")
		if err != nil {
			return target, fmt.Errorf("unable to resolve the PR repository from origin's push URL: %w", err)
		}
		var repo struct {
			URL           string `json:"url"`
			DefaultBranch struct {
				Name string `json:"name"`
			} `json:"defaultBranchRef"`
		}
		if json.Unmarshal([]byte(metadata), &repo) != nil || repo.URL == "" || repo.DefaultBranch.Name == "" {
			return target, errors.New("origin's repository must have a URL and a default branch for PR creation")
		}
		target.URL, target.Base = repo.URL, repo.DefaultBranch.Name
	}
	if pr || branch != "" {
		current, err := s.Branch(ctx)
		if err != nil {
			return target, err
		}
		target.Branch = current
	}
	if branch != "" && branch != target.Branch {
		if _, err := s.run(ctx, "git", "check-ref-format", "--branch", branch); err != nil {
			return target, err
		}
		if err := s.Clean(ctx); err != nil {
			return target, fmt.Errorf("cannot switch to branch %q: %w", branch, err)
		}
		var args []string
		if _, err := s.run(ctx, "git", "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
			args = []string{"checkout", branch, "--"}
		} else if _, err := s.run(ctx, "git", "show-ref", "--verify", "--quiet", "refs/remotes/origin/"+branch); err == nil {
			args = []string{"checkout", "--track", "origin/" + branch}
		} else {
			args = []string{"checkout", "-b", branch}
		}
		if _, err := s.run(ctx, "git", args...); err != nil {
			return target, err
		}
		target.Branch = branch
		if pr {
			if err := s.Clean(ctx); err != nil {
				return target, err
			}
		}
	}
	return target, nil
}

func (s Service) Publish(ctx context.Context, target Target, files []string, title, body string) error {
	if len(files) == 0 {
		return nil
	}
	staged, err := s.run(ctx, "git", "diff", "--cached", "--no-relative", "--name-only", "-z")
	if err != nil {
		return err
	}
	if staged != "" {
		return errors.New("refusing to commit unrelated staged changes")
	}
	prefix, err := s.run(ctx, "git", "rev-parse", "--show-prefix")
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, file := range files {
		if !seen[prefix+filepath.ToSlash(file)] {
			if _, err := s.run(ctx, "git", "add", "--", file); err != nil {
				return err
			}
			seen[prefix+filepath.ToSlash(file)] = true
		}
	}
	changed, err := s.run(ctx, "git", "diff", "--cached", "--no-relative", "--name-only", "-z")
	if err != nil {
		return err
	}
	if changed == "" {
		return nil
	}
	for _, file := range strings.Split(strings.TrimSuffix(changed, "\x00"), "\x00") {
		if !seen[file] {
			return fmt.Errorf("refusing to commit unexpected staged path %q", file)
		}
	}
	for _, command := range [][]string{{"git", "commit", "-m", title}, {"git", "push", "-u", "origin", target.Branch}, {"gh", "pr", "create", "--repo", target.URL, "--title", title, "--body", body, "--head", target.Branch, "--base", target.Base}} {
		out, err := s.run(ctx, command[0], command[1:]...)
		if err != nil {
			return err
		}
		if s.Output != nil && out != "" {
			if _, err := fmt.Fprintln(s.Output, out); err != nil {
				return err
			}
		}
	}
	return nil
}

func NormalizeURL(remote string) string {
	remote = strings.TrimSuffix(remote, ".git")
	if strings.HasPrefix(remote, "git@") {
		parts := strings.SplitN(strings.TrimPrefix(remote, "git@"), ":", 2)
		if len(parts) == 2 {
			return "https://" + parts[0] + "/" + parts[1]
		}
	}
	if strings.HasPrefix(remote, "ssh://git@") {
		return "https://" + strings.TrimPrefix(remote, "ssh://git@")
	}
	if u, err := url.Parse(remote); err == nil && u.User != nil {
		u.User = nil
		return u.String()
	}
	return remote
}

func (s Service) Identity(ctx context.Context) (string, string) {
	name := filepath.Base(s.Dir)
	if top, err := s.run(ctx, "git", "rev-parse", "--show-toplevel"); err == nil {
		name = filepath.Base(top)
	}
	remote, _ := s.run(ctx, "git", "remote", "get-url", "origin")
	return name, NormalizeURL(remote)
}

// Token mirrors gh's environment precedence without exposing credentials in arguments.
func Token(ctx context.Context, runner Runner, dir, host string, lookup func(string) (string, bool)) string {
	keys := []string{"GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN"}
	if host == "github.com" || strings.HasSuffix(host, ".ghe.com") {
		keys = []string{"GH_TOKEN", "GITHUB_TOKEN"}
	}
	for _, key := range keys {
		if value, _ := lookup(key); value != "" {
			return value
		}
	}
	token, err := runner.Run(ctx, dir, "gh", "auth", "token", "--hostname", host)
	if err != nil {
		return ""
	}
	return token
}
