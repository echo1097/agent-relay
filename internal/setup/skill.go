package setup

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"agent-relay/internal/fileedit"
)

func SkillPath(env Environment, clientName string) string {
	root := filepath.Join(env.Home, ".agents")
	if clientName == "claude" {
		root = env.Getenv("CLAUDE_CONFIG_DIR")
		if root == "" {
			root = filepath.Join(env.Home, ".claude")
		}
	}
	return filepath.Join(root, "skills", "agent-relay", "SKILL.md")
}

func SyncSkill(path string, remove bool) (string, error) {
	skillDir := filepath.Dir(path)
	info, err := os.Lstat(skillDir)
	if errors.Is(err, os.ErrNotExist) {
		if remove {
			return "already absent", nil
		}
		if err := os.MkdirAll(skillDir, 0700); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "preserved: skill directory is not a regular directory", nil
	}

	lockPath := filepath.Join(filepath.Dir(skillDir), ".agent-relay-skill.lock")
	lockFD, err := syscall.Open(lockPath, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return "", err
	}
	lockFile := os.NewFile(uintptr(lockFD), lockPath)
	defer lockFile.Close()
	if err := syscall.Flock(lockFD, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return "", fmt.Errorf("skill setup is busy; retry: %w", err)
	}

	data, _, err := fileedit.Read(path)
	if err != nil {
		return "", err
	}
	receiptPath := filepath.Join(skillDir, ".agent-relay-sha256")
	receipt, _, err := fileedit.Read(receiptPath)
	if err != nil {
		return "", err
	}
	managed := data != nil && strings.TrimSpace(string(receipt)) == skillHash(data)
	if remove {
		if data != nil && !managed {
			return "preserved: customized or unmanaged skill", nil
		}
		if data != nil {
			if err := os.Remove(path); err != nil {
				return "", err
			}
		}
		if err := os.Remove(receiptPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		entries, err := os.ReadDir(skillDir)
		if err != nil {
			return "", err
		}
		if len(entries) == 0 {
			if err := os.Remove(skillDir); err != nil {
				return "", err
			}
		}
		return "removed or already absent", nil
	}

	content := []byte(skillContent)
	if data != nil && !managed && !bytes.Equal(data, content) {
		return "preserved: customized or unmanaged skill", nil
	}
	if !bytes.Equal(data, content) {
		if err := writeSkillFile(path, content); err != nil {
			return "", err
		}
	}
	if strings.TrimSpace(string(receipt)) != skillHash(content) {
		if err := writeSkillFile(receiptPath, []byte(skillHash(content)+"\n")); err != nil {
			return "", err
		}
	}
	return "installed and up to date", nil
}

func skillHash(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func writeSkillFile(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".agent-relay-skill-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(data)
	if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
