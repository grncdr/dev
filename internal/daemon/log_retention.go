package daemon

import (
	"fmt"
	"io"
	"os"
)

const managedLogMaxBytes int64 = 20 * 1024 * 1024

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
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	if len(tail) > 0 {
		if _, err := file.Write(tail); err != nil {
			return 0, err
		}
	}
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		return 0, err
	}
	return int64(len(tail)), nil
}

func appendOpenFileWithRetention(file *os.File, currentSize int64, data []byte, maxBytes int64) (int64, error) {
	if file == nil {
		return currentSize, nil
	}
	if maxBytes <= 0 {
		if _, err := file.Write(data); err != nil {
			return currentSize, err
		}
		return currentSize + int64(len(data)), nil
	}
	if int64(len(data)) >= maxBytes {
		data = data[len(data)-int(maxBytes):]
		currentSize = 0
	}
	if currentSize+int64(len(data)) <= maxBytes {
		if _, err := file.Seek(0, io.SeekEnd); err != nil {
			return currentSize, err
		}
		n, err := file.Write(data)
		return currentSize + int64(n), err
	}
	retain := maxBytes - int64(len(data))
	if retain < 0 {
		retain = 0
	}
	tail, err := readTailFromFile(file, retain)
	if err != nil {
		return currentSize, err
	}
	if err := file.Truncate(0); err != nil {
		return currentSize, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return currentSize, err
	}
	if len(tail) > 0 {
		if _, err := file.Write(tail); err != nil {
			return currentSize, err
		}
	}
	if len(data) > 0 {
		if _, err := file.Write(data); err != nil {
			return currentSize, err
		}
	}
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		return currentSize, err
	}
	return int64(len(tail) + len(data)), nil
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
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	buf := make([]byte, n)
	_, err = io.ReadFull(file, buf)
	if err != nil {
		return nil, err
	}
	return buf, nil
}

func appendLineToCappedPath(path, line string, maxBytes int64) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	size, err := trimOpenFileToMax(file, maxBytes)
	if err != nil {
		return err
	}
	_, err = appendOpenFileWithRetention(file, size, []byte(line), maxBytes)
	return err
}
