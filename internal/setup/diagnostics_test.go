package setup

import (
	"encoding/json"
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

func TestCheckUsesEffectiveMCPArguments(t *testing.T) {
	home := t.TempDir()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	client := Client{Name: "claude", Path: filepath.Join(home, "claude.json")}
	for _, testCase := range []struct {
		args  []string
		valid bool
	}{
		{[]string{"mcp", "--home=" + home}, true},
		{[]string{"mcp", "--home", home, "--home", home + "other"}, false},
		{[]string{"mcp", "--home", home, "--unknown"}, false},
		{[]string{"mcp", "--home", home, "extra"}, false},
	} {
		data, err := json.Marshal(map[string]any{"mcpServers": map[string]any{"agent-relay": map[string]any{"command": binary, "args": testCase.args}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(client.Path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if err := Check(client, home); (err == nil) != testCase.valid {
			t.Fatalf("args %v: %v", testCase.args, err)
		}
	}
}
