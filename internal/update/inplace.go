package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// commitInPlace retains the existing inode on platforms whose ACLs cannot be
// enumerated as xattrs. It matches the original copy-into-existing-file behavior.
func (p *Plan) commitInPlace(ctx context.Context, directory string, write func(*os.File, []byte) error) error {
	if len(p.Changes) == 0 {
		return nil
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	backupPath, err := os.MkdirTemp("", "actions-snitch-originals-")
	if err != nil {
		return err
	}
	keepBackups := false
	defer func() {
		if !keepBackups {
			_ = os.RemoveAll(backupPath)
		}
	}()
	backups, err := os.OpenRoot(backupPath)
	if err != nil {
		return err
	}
	defer func() { _ = backups.Close() }()
	for i, change := range p.Changes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := unchanged(root, change); err != nil {
			return err
		}
		// The descriptor check authorizes writes without changing source bytes.
		file, err := openOriginal(root, change)
		if err != nil {
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
		if err := backups.WriteFile(strconv.Itoa(i), change.File.Original, 0600); err != nil {
			return err
		}
	}
	if write == nil {
		write = writeContents
	}
	lastModified := -1
	rollback := func(cause error) error {
		for i := lastModified; i >= 0; i-- {
			change := p.Changes[i]
			if err := writeOriginal(root, change, change.File.Original, write); err != nil {
				keepBackups = true
				cause = errors.Join(cause, fmt.Errorf("rollback failed for %s; original retained at %s: %w", change.File.Path, filepath.Join(backupPath, strconv.Itoa(i)), err))
			}
		}
		return cause
	}
	for i, change := range p.Changes {
		if err := ctx.Err(); err != nil {
			return rollback(err)
		}
		if err := unchanged(root, change); err != nil {
			return rollback(err)
		}
		// A failed write can still have changed a prefix, so include it in rollback.
		lastModified = i
		if err := writeOriginal(root, change, change.Data, write); err != nil {
			return rollback(fmt.Errorf("publish %s: %w", change.File.Path, err))
		}
	}
	if err := ctx.Err(); err != nil {
		return rollback(err)
	}
	return nil
}

func openOriginal(root *os.Root, change Change) (*os.File, error) {
	file, err := root.OpenFile(change.File.Path, os.O_WRONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("workflow is not writable: %s: %w", change.File.Path, err)
	}
	info, err := file.Stat()
	if err != nil || !os.SameFile(info, change.File.Info) {
		_ = file.Close()
		return nil, fmt.Errorf("workflow identity changed: %s", change.File.Path)
	}
	return file, nil
}

func writeOriginal(root *os.Root, change Change, data []byte, write func(*os.File, []byte) error) error {
	file, err := openOriginal(root, change)
	if err != nil {
		return err
	}
	err = write(file, data)
	return errors.Join(err, file.Close())
}

func writeContents(file *os.File, data []byte) error {
	if _, err := file.WriteAt(data, 0); err != nil {
		return err
	}
	if err := file.Truncate(int64(len(data))); err != nil {
		return err
	}
	return file.Sync()
}
