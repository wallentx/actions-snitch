//go:build linux || darwin

package update

import (
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

type fileMetadata struct {
	UID, GID   uint32
	Mode       os.FileMode
	Attributes map[string][]byte
}

func fileDescriptor(file *os.File) (int, error) {
	fd := file.Fd()
	if fd > uintptr(math.MaxInt) {
		return 0, fmt.Errorf("invalid file descriptor")
	}
	return int(fd), nil
}

func metadata(file *os.File) (fileMetadata, error) {
	fd, err := fileDescriptor(file)
	if err != nil {
		return fileMetadata{}, err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return fileMetadata{}, err
	}
	info, err := file.Stat()
	if err != nil {
		return fileMetadata{}, err
	}
	result := fileMetadata{UID: stat.Uid, GID: stat.Gid, Mode: info.Mode(), Attributes: map[string][]byte{}}
	size, err := unix.Flistxattr(fd, nil)
	if errors.Is(err, unix.ENOTSUP) {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("read extended attribute names: %w", err)
	}
	if size > 1<<20 {
		return result, fmt.Errorf("extended attribute list exceeds limit")
	}
	names := make([]byte, size)
	size, err = unix.Flistxattr(fd, names)
	if err != nil {
		return result, err
	}
	for _, name := range strings.Split(string(names[:size]), "\x00") {
		if name == "" {
			continue
		}
		size, err := unix.Fgetxattr(fd, name, nil)
		if err != nil {
			return result, fmt.Errorf("read extended attribute %s: %w", name, err)
		}
		if size > 1<<20 {
			return result, fmt.Errorf("extended attribute exceeds limit")
		}
		value := make([]byte, size)
		size, err = unix.Fgetxattr(fd, name, value)
		if err != nil {
			return result, err
		}
		result.Attributes[name] = value[:size]
	}
	return result, nil
}

func preserveMetadata(file *os.File, original fileMetadata) error {
	fd, err := fileDescriptor(file)
	if err != nil {
		return err
	}
	var current unix.Stat_t
	if err := unix.Fstat(fd, &current); err != nil {
		return err
	}
	if current.Uid != original.UID || current.Gid != original.GID {
		if uint64(original.UID) > uint64(math.MaxInt) || uint64(original.GID) > uint64(math.MaxInt) {
			return fmt.Errorf("file ownership cannot be represented")
		}
		if err := file.Chown(int(original.UID), int(original.GID)); err != nil {
			return fmt.Errorf("preserve file ownership: %w", err)
		}
	}
	stagedMetadata, err := metadata(file)
	if err != nil {
		return err
	}
	for name := range stagedMetadata.Attributes {
		if _, present := original.Attributes[name]; !present {
			if err := unix.Fremovexattr(fd, name); err != nil {
				return fmt.Errorf("remove inherited extended attribute %s: %w", name, err)
			}
		}
	}
	if err := file.Chmod(original.Mode); err != nil {
		return err
	}
	names := make([]string, 0, len(original.Attributes))
	for name := range original.Attributes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := unix.Fsetxattr(fd, name, original.Attributes[name], 0); err != nil {
			return fmt.Errorf("preserve extended attribute %s: %w", name, err)
		}
	}
	return nil
}
