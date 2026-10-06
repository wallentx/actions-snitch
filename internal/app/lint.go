package app

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/wallentx/actions-snitch/internal/config"
	"github.com/wallentx/actions-snitch/internal/lintrepair"
	"github.com/wallentx/actions-snitch/internal/llm"
	"github.com/wallentx/actions-snitch/internal/update"
	"github.com/wallentx/actions-snitch/internal/workflow"
	"golang.org/x/term"
)

func executeLint(ctx context.Context, r Runtime) error {
	if !r.Interactive {
		return errors.New("--lint requires an interactive terminal for review and acceptance")
	}
	tool, _ := r.Lookup("DIFFTOOL")
	if tool == "" {
		return errors.New("--lint requires DIFFTOOL to name an executable such as dplex")
	}
	if _, err := exec.LookPath(tool); err != nil {
		return errors.New("DIFFTOOL must name one executable; use a wrapper script for arguments")
	}
	cfg, err := config.Load(config.Path(r.Lookup), r.Lookup)
	if err != nil {
		return err
	}
	service := llm.Service{Model: r.Model, Config: cfg.AI}
	if service.Model == nil {
		provider, endpoint, err := llm.NewProvider(ctx, cfg.AI, r.Lookup)
		if err != nil {
			return err
		}
		defer func() { _ = llm.Close(provider) }()
		service.Model, service.Endpoint = provider, endpoint
	}
	paths, err := workflow.Discover(r.Dir)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(r.Dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	plan := &update.Plan{}
	reader := bufio.NewReader(r.Input)
	for _, path := range paths {
		if !strings.HasPrefix(path, ".github/workflows/") {
			continue
		}
		info, err := root.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > lintrepair.MaxBytes {
			return errors.New("lint source is not a bounded regular file")
		}
		source, err := root.ReadFile(path)
		if err != nil {
			return err
		}
		diagnostics, err := lintrepair.Validate(ctx, r.Dir, path, source)
		if err != nil {
			return err
		}
		if diagnostics == "" {
			continue
		}
		_, _ = fmt.Fprintf(r.Error, "Lint found structural problems in %s:\n%s\n", llm.SafeSummary(path), diagnostics)
		evidence, err := lintrepair.Prepare(source)
		if err != nil {
			return err
		}
		proposal, err := service.RepairLint(ctx, evidence.Text, diagnostics)
		if err != nil {
			return err
		}
		candidate, err := evidence.Restore(proposal)
		if err != nil {
			return err
		}
		if bytes.Equal(source, candidate) {
			return errors.New("the model proposed no repair")
		}
		if err := lintrepair.Check(ctx, r.Dir, path, candidate); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(r.Error, "The proposed repair passes YAML and actionlint checks. These checks cannot prove intended behavior; review the changes carefully. The diff tool edits a temporary copy.")
		selected, err := reviewLint(ctx, r, tool, source, candidate)
		if err != nil {
			return err
		}
		if bytes.Equal(selected, source) {
			continue
		}
		if err := lintrepair.CheckSelection(source, candidate, selected); err != nil {
			return err
		}
		if err := lintrepair.Check(ctx, r.Dir, path, selected); err != nil {
			return err
		}
		answer, err := prompt(ctx, reader, r, "Apply this repair when all reviews finish? [y/N]: ")
		if err != nil {
			return context.Canceled
		}
		if strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes") {
			plan.Changes = append(plan.Changes, update.Change{File: &workflow.File{Path: path, Original: source, Info: info, Mode: info.Mode()}, Data: selected, Count: 1})
		}
	}
	if err := plan.Commit(ctx, r.Dir); err != nil {
		return err
	}
	_, err = fmt.Fprintf(r.Output, "Applied %d lint repair(s). No action upgrades were performed.\n", len(plan.Changes))
	return err
}

func reviewLint(ctx context.Context, r Runtime, tool string, original, proposed []byte) ([]byte, error) {
	dir, err := os.MkdirTemp("", "actions-snitch-lint-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	local, remote := filepath.Join(dir, "current.yml"), filepath.Join(dir, "proposed.yml")
	tempRoot, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tempRoot.Close() }()
	if err := tempRoot.WriteFile("current.yml", original, 0600); err != nil {
		return nil, err
	}
	if err := tempRoot.WriteFile("proposed.yml", proposed, 0600); err != nil {
		return nil, err
	}
	if input, ok := r.Input.(*os.File); ok {
		descriptor := input.Fd()
		if descriptor > uintptr(math.MaxInt) {
			return nil, errors.New("terminal descriptor exceeds supported range")
		}
		fd := int(descriptor)
		if term.IsTerminal(fd) {
			state, err := term.GetState(fd)
			if err != nil {
				return nil, err
			}
			defer func() { _ = term.Restore(fd, state) }()
		}
	}
	// #nosec G204 -- DIFFTOOL is explicitly selected by the user; paths are separate arguments and no shell is invoked.
	cmd := exec.CommandContext(ctx, tool, local, remote)
	cmd.WaitDelay = time.Second
	cmd.Stdin = r.Input
	cmd.Stdout = r.Error
	cmd.Stderr = r.Error
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("diff review was cancelled or failed; no files were changed")
	}
	file, err := tempRoot.Open("current.yml")
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > lintrepair.MaxBytes {
		return nil, errors.New("diff result is not a bounded regular file")
	}
	selected, err := io.ReadAll(io.LimitReader(file, lintrepair.MaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(selected) > lintrepair.MaxBytes {
		return nil, errors.New("diff result exceeds size limit")
	}
	return selected, nil
}
