package daemon

import (
	"fmt"
	"os"

	"dev/internal/logfile"
)

// managedLogMaxBytes is the per-file rotation threshold for managed logs.
// Combined with one rotated copy at <path>+logfile.RotatedSuffix, the total
// retained per stream is bounded at 2*managedLogMaxBytes.
const managedLogMaxBytes int64 = 10 * 1024 * 1024

// rotateAndReopen renames path to path+RotatedSuffix (overwriting any prior
// rotation) and opens a fresh file at path. The original file is closed only
// after the rename and reopen succeed, so the caller's file handle remains
// valid on error and can continue to be used.
func rotateAndReopen(path string, file *os.File) (*os.File, error) {
	rotated := path + logfile.RotatedSuffix
	if err := os.Rename(path, rotated); err != nil && !os.IsNotExist(err) {
		return file, fmt.Errorf("rotate log: %w", err)
	}
	fresh, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return file, fmt.Errorf("open new log: %w", err)
	}
	_ = file.Close()
	return fresh, nil
}

// appendWithRotation appends data to file, rotating to path+RotatedSuffix
// before the write if currentSize+len(data) would exceed maxBytes. The cost
// per call is O(len(data)) regardless of file size, replacing the older
// O(maxBytes) rewrite-in-place scheme.
func appendWithRotation(path string, file *os.File, currentSize int64, data []byte, maxBytes int64) (*os.File, int64, error) {
	if file == nil {
		return nil, currentSize, fmt.Errorf("log file is nil")
	}
	if len(data) == 0 {
		return file, currentSize, nil
	}
	if maxBytes > 0 && currentSize+int64(len(data)) > maxBytes {
		newFile, err := rotateAndReopen(path, file)
		if err != nil {
			return file, currentSize, err
		}
		file = newFile
		currentSize = 0
	}
	if _, err := file.Seek(0, 2); err != nil {
		return file, currentSize, err
	}
	n, err := file.Write(data)
	return file, currentSize + int64(n), err
}

// trimOpenFileToMax keeps the last maxBytes of an oversized log on disk.
// Used at startup so a process that crashed with a runaway log gets bounded
// before normal append-with-rotation takes over.
func trimOpenFileToMax(file *os.File, maxBytes int64) (int64, error) {
	if file == nil {
		return 0, fmt.Errorf("log file is nil")
	}
	if maxBytes <= 0 {
		return 0, nil
	}
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	size := info.Size()
	if size <= maxBytes {
		return size, nil
	}
	tail, err := readTailFromFile(file, maxBytes)
	if err != nil {
		return 0, err
	}
	if err := file.Truncate(0); err != nil {
		return 0, err
	}
	if _, err := file.Seek(0, 0); err != nil {
		return 0, err
	}
	if len(tail) > 0 {
		if _, err := file.Write(tail); err != nil {
			return 0, err
		}
	}
	if _, err := file.Seek(0, 2); err != nil {
		return 0, err
	}
	return int64(len(tail)), nil
}

func readTailFromFile(file *os.File, n int64) ([]byte, error) {
	if n <= 0 {
		return nil, nil
	}
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
	if _, err := file.Seek(start, 0); err != nil {
		return nil, err
	}
	buf := make([]byte, n)
	if _, err := file.Read(buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// appendLineToCappedPath appends line to path, rotating to
// path+RotatedSuffix when the append would push past maxBytes. Used for
// line-oriented logs that aren't kept open between writes (daemon log).
func appendLineToCappedPath(path, line string, maxBytes int64) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	if maxBytes > 0 && info.Size()+int64(len(line)) > maxBytes {
		newFile, rerr := rotateAndReopen(path, file)
		if rerr != nil {
			_ = file.Close()
			return rerr
		}
		file = newFile
	}
	defer file.Close()
	if _, err := file.Seek(0, 2); err != nil {
		return err
	}
	_, err = file.Write([]byte(line))
	return err
}
