package setup

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestConfigurePreservesSettingsAndBacksUp(t *testing.T) {
	for _, clientName := range []string{"codex", "claude"} {
		t.Run(clientName, func(t *testing.T) {
			oldData := []byte("title = 'keep me'\n[mcp_servers.other]\ncommand = 'other'\nargs = ['a']\n")
			if clientName == "claude" {
				oldData = []byte(`{"counter":9007199254740993,"projects":{"x":{"mcpServers":{"local":{"command":"keep"}}}},"mcpServers":{"other":{"command":"other"}}}`)
			}
			path := filepath.Join(t.TempDir(), "config")
			if err := os.WriteFile(path, oldData, 0640); err != nil {
				t.Fatal(err)
			}
			client := Client{Name: clientName, Path: path}
			result, err := Configure(client, "/some path/relay", "/relay home", false)
			if err != nil || !result.Changed || result.Backup == "" {
				t.Fatalf("result: %+v, %v", result, err)
			}
			backup, err := os.ReadFile(result.Backup)
			if err != nil || !bytes.Equal(backup, oldData) {
				t.Fatal("backup is not the original config", err)
			}
			info, _ := os.Stat(result.Backup)
			if info.Mode().Perm() != 0600 {
				t.Fatal("backup permissions")
			}
			data, _ := os.ReadFile(path)
			info, _ = os.Stat(path)
			if info.Mode().Perm() != 0640 {
				t.Fatal("original permissions changed")
			}
			var root map[string]any
			if clientName == "codex" {
				if _, err := toml.Decode(string(data), &root); err != nil {
					t.Fatal(err)
				}
				if root["title"] != "keep me" || root["mcp_servers"].(map[string]any)["other"].(map[string]any)["command"] != "other" {
					t.Fatal("lost unrelated settings")
				}
			} else {
				decoder := json.NewDecoder(bytes.NewReader(data))
				decoder.UseNumber()
				if err := decoder.Decode(&root); err != nil {
					t.Fatal(err)
				}
				if root["counter"].(json.Number).String() != "9007199254740993" || root["projects"] == nil || root["mcpServers"].(map[string]any)["other"] == nil {
					t.Fatal("lost unrelated settings")
				}
			}
			result, err = Configure(client, "/some path/relay", "/relay home", false)
			if err != nil || result.Changed || result.Backup != "" {
				t.Fatalf("not idempotent: %+v %v", result, err)
			}
			if _, err := Configure(client, "/new/relay", "/relay home", false); err == nil {
				t.Fatal("overwrote conflict")
			}
			unchanged, _ := os.ReadFile(path)
			if !bytes.Equal(unchanged, data) {
				t.Fatal("conflict changed file")
			}
			result, err = Configure(client, "/new/relay", "/relay home", true)
			if err != nil || !result.Changed {
				t.Fatal("explicit replacement failed", err)
			}
		})
	}
}

func TestMalformedConfigIsUntouched(t *testing.T) {
	cases := []struct{ name, data string }{
		{"claude", ""},
		{"codex", "[broken"}, {"codex", "mcp_servers = 3"},
		{"claude", "{"}, {"claude", "null"}, {"claude", `{"mcpServers":null}`},
		{"claude", `{"a":1,"a":2}`}, {"claude", `{"mcpServers":{"x":{},"x":{}}}`}, {"claude", "{} {}"},
	}
	for _, testCase := range cases {
		path := filepath.Join(t.TempDir(), "config")
		os.WriteFile(path, []byte(testCase.data), 0600)
		result, err := Configure(Client{Name: testCase.name, Path: path}, "/relay", "/data", true)
		if err == nil || result.Changed || result.Backup != "" {
			t.Fatalf("accepted %s", testCase.data)
		}
		data, _ := os.ReadFile(path)
		if string(data) != testCase.data {
			t.Fatal("malformed config changed")
		}
	}
}

func TestDetect(t *testing.T) {
	env := Environment{Home: "/home/test", Getenv: func(key string) string {
		return map[string]string{"CODEX_HOME": "/custom/codex", "CLAUDE_CONFIG_DIR": "/custom/claude"}[key]
	}, LookPath: func(name string) (string, error) { return "", os.ErrNotExist }, Exists: func(path string) bool { return path == "/custom/claude/.claude.json" }}
	clients, err := Detect(env)
	if err != nil || clients[0].Detected || !clients[1].Detected || clients[0].Path != "/custom/codex/config.toml" || clients[1].Path != "/custom/claude/.claude.json" {
		t.Fatalf("%+v %v", clients, err)
	}
	env.Getenv = func(string) string { return "" }
	env.LookPath = func(name string) (string, error) { return "/bin/" + name, nil }
	clients, err = Detect(env)
	if err != nil || !clients[0].Detected || !clients[1].Detected || clients[1].Path != "/home/test/.claude.json" {
		t.Fatalf("%+v %v", clients, err)
	}
}

func TestUnsafeFile(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target")
	os.WriteFile(target, []byte("{}"), 0600)
	link := target + "-link"
	os.Symlink(target, link)
	if _, err := Configure(Client{Name: "claude", Path: link}, "/relay", "/data", false); err == nil || !strings.Contains(err.Error(), "symlinks") {
		t.Fatal(err)
	}
	if _, err := Configure(Client{Name: "claude", Path: target + "/nested"}, "/relay", "/data", false); err == nil {
		t.Fatal("accepted inaccessible path")
	}
}
