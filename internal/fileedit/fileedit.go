package fileedit

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

type Result struct {
	Changed bool
	Backup  string
}

func Read(path string) ([]byte, os.FileInfo, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("inspect %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%s must be a regular file; symlinks are not edited", path)
	}
	if info.Size() > 8<<20 {
		return nil, nil, fmt.Errorf("%s exceeds the 8 MiB configuration limit", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", path, err)
	}
	return data, info, nil
}

func Update(path string, transform func([]byte) ([]byte, error)) (result Result, returnErr error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return result, fmt.Errorf("create config directory: %w", err)
	}
	lockPath := path + ".agent-relay.lock"
	lockFD, err := syscall.Open(lockPath, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return result, fmt.Errorf("open configuration lock %s: %w", lockPath, err)
	}
	lockFile := os.NewFile(uintptr(lockFD), lockPath)
	defer lockFile.Close()
	if err := syscall.Flock(lockFD, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return result, fmt.Errorf("configuration busy at %s; retry after the other setup finishes: %w", path, err)
	}
	oldData, oldInfo, err := Read(path)
	if err != nil {
		return result, err
	}
	newData, err := transform(oldData)
	if err != nil {
		return result, fmt.Errorf("configure %s: %w", path, err)
	}
	if bytes.Equal(oldData, newData) {
		return result, nil
	}
	mode := os.FileMode(0600)
	if oldInfo != nil {
		mode = oldInfo.Mode().Perm()
		backup, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".agent-relay-backup-*")
		if err != nil {
			return result, fmt.Errorf("back up %s: %w", path, err)
		}
		result.Backup = backup.Name()
		writeErr := writeFile(backup, oldData)
		if writeErr != nil {
			return result, fmt.Errorf("write backup %s: %w", result.Backup, writeErr)
		}
	}
	tempFile, err := os.CreateTemp(filepath.Dir(path), ".agent-relay-write-*")
	if err != nil {
		return result, err
	}
	defer os.Remove(tempFile.Name())
	if err := tempFile.Chmod(mode); err != nil {
		tempFile.Close()
		return result, err
	}
	if err := writeFile(tempFile, newData); err != nil {
		return result, err
	}
	currentData, currentInfo, err := Read(path)
	if err != nil {
		return result, err
	}
	if !bytes.Equal(oldData, currentData) || (oldInfo == nil) != (currentInfo == nil) || oldInfo != nil && (!os.SameFile(oldInfo, currentInfo) || oldInfo.Mode() != currentInfo.Mode()) {
		return result, fmt.Errorf("%s changed during setup; close the client and retry", path)
	}
	if err := os.Rename(tempFile.Name(), path); err != nil {
		return result, fmt.Errorf("replace %s: %w", path, err)
	}
	result.Changed = true
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return result, err
	}
	return result, errors.Join(dir.Sync(), dir.Close())
}

func writeFile(file *os.File, data []byte) error {
	count, err := file.Write(data)
	if err == nil && count != len(data) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = file.Sync()
	}
	return errors.Join(err, file.Close())
}
