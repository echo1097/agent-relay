package setup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckMCPConfiguration(t *testing.T) {
	home := t.TempDir()
	binary := filepath.Join(home, "relay")
	if err := os.WriteFile(binary, []byte("executable placeholder"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"codex", "claude"} {
		t.Run(name, func(t *testing.T) {
			client := Client{Name: name, Path: filepath.Join(home, name)}
			if err := Check(client, home); err == nil {
				t.Fatal("missing config accepted")
			}
			if _, err := Configure(client, binary, home, false); err != nil {
				t.Fatal(err)
			}
			if err := Check(client, home); err != nil {
				t.Fatal(err)
			}
			if err := Check(client, home+"other"); err == nil {
				t.Fatal("wrong home accepted")
			}
			if err := os.Chmod(binary, 0600); err != nil {
				t.Fatal(err)
			}
			if err := Check(client, home); err == nil {
				t.Fatal("nonexecutable accepted")
			}
			if err := os.Chmod(binary, 0700); err != nil {
				t.Fatal(err)
			}
		})
	}
}
