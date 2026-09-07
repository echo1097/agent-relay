package service

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func (manager *Manager) withLock(action func() error) error {
	path := filepath.Join(manager.installDir(), "service.lock")
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("another service command is running; retry when it finishes: %w", err)
	}
	return action()
}
