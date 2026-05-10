// Package logfile provides shared helpers for managed process and daemon
// logs that are kept under a rotation scheme: a current file at <path> and
// at most one rotated copy at <path>+RotatedSuffix. Writers (in
// internal/daemon) rotate by renaming; readers (in internal/cli) stitch the
// two files into a single logical stream.
package logfile

import (
	"errors"
	"io"
	"os"
)

// RotatedSuffix is appended to a managed log path to identify the most
// recent rotated copy.
const RotatedSuffix = ".1"

// ReadAll returns the concatenation of <path>+RotatedSuffix (if present) and
// <path>. Returns os.ErrNotExist only when neither file exists.
func ReadAll(path string) ([]byte, error) {
	rotated, rotErr := os.ReadFile(path + RotatedSuffix)
	if rotErr != nil && !errors.Is(rotErr, os.ErrNotExist) {
		return nil, rotErr
	}
	current, curErr := os.ReadFile(path)
	if curErr != nil {
		if errors.Is(curErr, os.ErrNotExist) {
			if len(rotated) == 0 {
				return nil, curErr
			}
			return rotated, nil
		}
		return nil, curErr
	}
	if len(rotated) == 0 {
		return current, nil
	}
	out := make([]byte, 0, len(rotated)+len(current))
	out = append(out, rotated...)
	out = append(out, current...)
	return out, nil
}

// ReadTail returns up to n bytes from the end of the logical stream
// (<path>+RotatedSuffix followed by <path>). Returns os.ErrNotExist only
// when neither file exists.
func ReadTail(path string, n int64) ([]byte, error) {
	if n <= 0 {
		return nil, nil
	}
	current, curErr := readTail(path, n)
	if curErr != nil && !errors.Is(curErr, os.ErrNotExist) {
		return nil, curErr
	}
	if int64(len(current)) >= n {
		return current, nil
	}
	remaining := n - int64(len(current))
	rotated, rotErr := readTail(path+RotatedSuffix, remaining)
	if rotErr != nil {
		if errors.Is(rotErr, os.ErrNotExist) {
			if curErr != nil {
				return nil, curErr
			}
			return current, nil
		}
		return nil, rotErr
	}
	if len(rotated) == 0 {
		return current, nil
	}
	out := make([]byte, 0, len(rotated)+len(current))
	out = append(out, rotated...)
	out = append(out, current...)
	return out, nil
}

func readTail(path string, n int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	if size <= 0 {
		return nil, nil
	}
	if n > size {
		n = size
	}
	start := size - n
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(file, buf); err != nil {
		return nil, err
	}
	return buf, nil
}
