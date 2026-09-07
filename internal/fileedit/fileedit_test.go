package fileedit

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConcurrentChangeAndLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	os.WriteFile(path, []byte("before"), 0600)
	result, err := Update(path, func(data []byte) ([]byte, error) {
		if _, err := Update(path, func(data []byte) ([]byte, error) { return []byte("nested"), nil }); err == nil || !strings.Contains(err.Error(), "busy") {
			t.Fatalf("lock: %v", err)
		}
		if err := os.WriteFile(path, []byte("client write"), 0600); err != nil {
			t.Fatal(err)
		}
		return []byte("after"), nil
	})
	if err == nil || result.Changed || !strings.Contains(err.Error(), "changed during setup") {
		t.Fatalf("%+v %v", result, err)
	}
	data, _ := os.ReadFile(path)
	if !bytes.Equal(data, []byte("client write")) {
		t.Fatal("lost client change")
	}
	backup, _ := os.ReadFile(result.Backup)
	if string(backup) != "before" {
		t.Fatal("backup changed")
	}
}

func TestLockSymlink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	target := path + "-target"
	os.WriteFile(target, []byte("keep"), 0600)
	os.Symlink(target, path+".agent-relay.lock")
	if _, err := Update(path, func(data []byte) ([]byte, error) { return []byte("new"), nil }); err == nil {
		t.Fatal("accepted symlink lock")
	}
	data, _ := os.ReadFile(target)
	if string(data) != "keep" {
		t.Fatal("modified target")
	}
}
