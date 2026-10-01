//go:build !linux && !darwin

package update

import "os"

type fileMetadata struct{ Mode os.FileMode }

func metadata(file *os.File) (fileMetadata, error) {
	info, err := file.Stat()
	if err != nil {
		return fileMetadata{}, err
	}
	return fileMetadata{Mode: info.Mode()}, nil
}
func preserveMetadata(file *os.File, original fileMetadata) error { return file.Chmod(original.Mode) }
