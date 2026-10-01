// Package cache provides the 24-hour XDG disk cache.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"time"
)

const TTL = 24 * time.Hour

type Store struct {
	Dir string
	Now func() time.Time
}

func Key(query string) string { sum := sha256.Sum256([]byte(query)); return hex.EncodeToString(sum[:]) }

func (s Store) Get(query string) ([]byte, bool) {
	directory, err := os.OpenRoot(s.Dir)
	if err != nil {
		return nil, false
	}
	defer func() { _ = directory.Close() }()
	path := "go-" + Key(query)
	info, err := directory.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, false
	}
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	if now.Sub(info.ModTime()) > TTL {
		return nil, false
	}
	b, err := directory.ReadFile(path)
	return b, err == nil
}

func (s Store) Put(query string, data []byte) error {
	if err := os.MkdirAll(s.Dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(s.Dir, ".cache-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer func() { _ = os.Remove(name) }()
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	return os.Rename(name, filepath.Join(s.Dir, "go-"+Key(query)))
}
