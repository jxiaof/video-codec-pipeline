package utils

import (
	"os"
	"sync"
	"syscall"
)

// FileLock provides a mechanism to lock a file.
type FileLock struct {
	file *os.File
	mu   sync.Mutex
}

// NewFileLock creates a new FileLock for the specified file path.
func NewFileLock(filePath string) (*FileLock, error) {
	file, err := os.OpenFile(filePath, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		return nil, err
	}
	return &FileLock{file: file}, nil
}

// Lock locks the file for exclusive access.
func (fl *FileLock) Lock() error {
	fl.mu.Lock()
	return syscall.Flock(int(fl.file.Fd()), syscall.LOCK_EX)
}

// Unlock unlocks the file.
func (fl *FileLock) Unlock() error {
	err := syscall.Flock(int(fl.file.Fd()), syscall.LOCK_UN)
	fl.mu.Unlock()
	return err
}

// Close closes the file.
func (fl *FileLock) Close() error {
	return fl.file.Close()
}
