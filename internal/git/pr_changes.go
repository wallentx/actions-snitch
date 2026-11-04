package git

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"
)

type FileChange struct {
	Path   string
	Before []byte
	After  []byte
}

// PRChanges includes committed branch updates and the current working changes.
// The fetched base belongs to the PR's push destination, not a different origin
// fetch URL. The merge base matches GitHub's three-dot PR comparison.
func (s Service) PRChanges(ctx context.Context, target Target) ([]FileChange, error) {
	if target.BaseRevision == "" {
		return nil, fmt.Errorf("PR base revision is unavailable")
	}
	base, err := s.run(ctx, "git", "merge-base", "HEAD", target.BaseRevision)
	if err != nil {
		return nil, fmt.Errorf("could not determine the PR merge base; fetch sufficient branch history: %w", err)
	}
	rootPath, err := s.run(ctx, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	changed, err := s.run(ctx, "git", "diff", "--no-relative", "--find-renames", "--name-status", "--diff-filter=AMR", "-z", base, "--")
	if err != nil {
		return nil, err
	}
	var changes []FileChange
	fields := strings.Split(strings.TrimSuffix(changed, "\x00"), "\x00")
	for i := 0; i < len(fields) && fields[i] != ""; {
		if i+1 >= len(fields) {
			return nil, fmt.Errorf("incomplete PR diff metadata")
		}
		status, name := fields[i], fields[i+1]
		beforePath := name
		i += 2
		if strings.HasPrefix(status, "R") {
			if i >= len(fields) {
				return nil, fmt.Errorf("incomplete PR rename metadata")
			}
			name = fields[i]
			i++
		}
		composite := path.Base(name) == "action.yml" || path.Base(name) == "action.yaml"
		workflow := strings.HasPrefix(name, ".github/workflows/") && (strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml"))
		if !composite && !workflow {
			continue
		}
		info, err := root.Lstat(name)
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("PR workflow %q is not a readable regular file", name)
		}
		var before string
		if status != "A" {
			before, err = s.run(ctx, "git", "show", base+":"+beforePath)
			if err != nil {
				return nil, err
			}
		}
		after, err := root.ReadFile(name)
		if err != nil {
			return nil, err
		}
		changes = append(changes, FileChange{Path: name, Before: []byte(before), After: after})
	}
	return changes, nil
}
