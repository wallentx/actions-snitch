package update

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
)

// Commit publishes prepared files only after checking the original snapshots.
// Linux replaces files atomically; macOS retains their inodes and native ACLs.
func (p *Plan) Commit(ctx context.Context, directory string) error {
	if runtime.GOOS == "darwin" {
		return p.commitInPlace(ctx, directory, nil)
	}
	return p.commit(ctx, directory, nil)
}

type staged struct {
	change                Change
	modified, backup      string
	published, keepBackup bool
	metadata              fileMetadata
}

func (p *Plan) commit(ctx context.Context, directory string, rename func(*os.Root, string, string) error) error {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	if rename == nil {
		rename = func(r *os.Root, from, to string) error { return r.Rename(from, to) }
	}
	var pending []*staged
	defer func() {
		for _, s := range pending {
			removeStaged(root, s.modified)
			if !s.keepBackup {
				removeStaged(root, s.backup)
			}
		}
	}()
	for _, change := range p.Changes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := unchanged(root, change); err != nil {
			return err
		}
		sourceMetadata, err := writableMetadata(root, change)
		if err != nil {
			return err
		}
		s := &staged{change: change, metadata: sourceMetadata}
		pending = append(pending, s)
		s.modified, err = stageFile(root, change.File.Path, change.Data, s.metadata)
		if err != nil {
			return err
		}
		s.backup, err = stageFile(root, change.File.Path, change.File.Original, s.metadata)
		if err != nil {
			return err
		}
	}
	rollback := func(cause error) error {
		for i := len(pending) - 1; i >= 0; i-- {
			s := pending[i]
			if !s.published {
				continue
			}
			if err := rename(root, s.backup, s.change.File.Path); err != nil {
				s.keepBackup = true
				cause = errors.Join(cause, fmt.Errorf("rollback failed for %s; original retained at %s: %w", s.change.File.Path, s.backup, err))
			}
		}
		return cause
	}
	for _, s := range pending {
		if err := ctx.Err(); err != nil {
			return rollback(err)
		}
		if err := unchanged(root, s.change); err != nil {
			return rollback(err)
		}
		currentMetadata, err := writableMetadata(root, s.change)
		if err != nil {
			return rollback(err)
		}
		if !reflect.DeepEqual(currentMetadata, s.metadata) {
			return rollback(fmt.Errorf("file metadata changed since staging: %s", s.change.File.Path))
		}
		if err := rename(root, s.modified, s.change.File.Path); err != nil {
			return rollback(fmt.Errorf("publish %s: %w", s.change.File.Path, err))
		}
		s.published = true
	}
	if err := ctx.Err(); err != nil {
		return rollback(err)
	}
	return nil
}

func unchanged(root *os.Root, change Change) error {
	path := change.File.Path
	if !filepath.IsLocal(path) || strings.ContainsAny(path, "\r\n\x00") {
		return fmt.Errorf("unsafe file path %q", path)
	}
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i := range parts {
		info, err := root.Lstat(filepath.Join(parts[:i+1]...))
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing symlink path %s", path)
		}
	}
	info, err := root.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || change.File.Info == nil || !os.SameFile(info, change.File.Info) || info.Mode() != change.File.Mode {
		return fmt.Errorf("file identity or mode changed since scan: %s", path)
	}
	current, err := root.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, change.File.Original) {
		return fmt.Errorf("file changed since scan: %s", path)
	}
	return nil
}

func stageFile(root *os.Root, target string, data []byte, original fileMetadata) (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	directory := filepath.Join(filepath.Dir(target), ".actions-snitch-"+hex.EncodeToString(random[:]))
	if err := root.Mkdir(directory, 0700); err != nil {
		return "", err
	}
	name := filepath.Join(directory, "content")
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		_ = root.Remove(directory)
		return "", err
	}
	if _, err = f.Write(data); err == nil {
		err = preserveMetadata(f, original)
	}
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		removeStaged(root, name)
		return "", err
	}
	return name, nil
}

// Opening without truncation preserves the original permission/ACL write check.
func writableMetadata(root *os.Root, change Change) (fileMetadata, error) {
	file, err := root.OpenFile(change.File.Path, os.O_WRONLY, 0)
	if err != nil {
		return fileMetadata{}, fmt.Errorf("workflow is not writable: %s: %w", change.File.Path, err)
	}
	info, statErr := file.Stat()
	if statErr != nil || !os.SameFile(info, change.File.Info) {
		_ = file.Close()
		return fileMetadata{}, fmt.Errorf("workflow identity changed: %s", change.File.Path)
	}
	original, err := metadata(file)
	return original, errors.Join(err, file.Close())
}

func removeStaged(root *os.Root, path string) {
	if path == "" {
		return
	}
	_ = root.Remove(path)
	_ = root.Remove(filepath.Dir(path))
}
