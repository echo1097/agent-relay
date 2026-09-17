package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agent-relay/internal/setup"
)

func TestSetupCLI(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	configPath := filepath.Join(root, "client.json")
	relayHome := filepath.Join(root, "relay")
	args := []string{"setup", "claude", "--config", configPath, "--home", relayHome}
	var output bytes.Buffer
	if err := Run(context.Background(), args, &output, &output, "test"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "claude: configured") {
		t.Fatal(output.String())
	}
	if _, err := os.Stat(relayHome); !os.IsNotExist(err) {
		t.Fatal("setup created Relay state")
	}
	output.Reset()
	if err := Run(context.Background(), args, &output, &output, "test"); err != nil || !strings.Contains(output.String(), "already configured") {
		t.Fatal(output.String(), err)
	}
	skillPath := filepath.Join(root, ".claude", "skills", "agent-relay", "SKILL.md")
	if _, err := os.Stat(skillPath); err != nil {
		t.Fatal("setup did not install the skill", err)
	}
	instructionPath := filepath.Join(root, ".claude", "CLAUDE.md")
	instructions, err := os.ReadFile(instructionPath)
	if err != nil || !strings.Contains(string(instructions), skillPath) {
		t.Fatal("setup did not install startup instructions", err)
	}
	if err := Run(context.Background(), append(args, "--remove"), &output, &output, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(skillPath); !os.IsNotExist(err) {
		t.Fatal("setup removal left the managed skill", err)
	}
	instructions, err = os.ReadFile(instructionPath)
	if err != nil || len(instructions) != 0 {
		t.Fatal("setup removal left startup instructions", err)
	}
	for _, badArgs := range [][]string{{"setup", "wrong"}, {"setup", "--config", configPath}, {"setup", "codex", "extra"}} {
		if err := Run(context.Background(), badArgs, &output, &output, "test"); err == nil {
			t.Fatal(badArgs)
		}
	}
}

func TestSetupRemovalCleansUpWithoutClientConfigs(t *testing.T) {
	for _, customPaths := range []bool{false, true} {
		name := "default paths"
		if customPaths {
			name = "overridden paths"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("HOME", root)
			t.Setenv("PATH", t.TempDir())
			t.Setenv("CODEX_HOME", "")
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			if customPaths {
				t.Setenv("CODEX_HOME", filepath.Join(root, "custom codex"))
				t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "custom claude"))
			}
			env, err := setup.LocalEnvironment()
			if err != nil {
				t.Fatal(err)
			}
			clients, err := setup.Detect(env)
			if err != nil {
				t.Fatal(err)
			}
			relayHome := filepath.Join(root, "relay")
			originalFiles := map[string][]byte{}
			var skillPaths []string
			var output bytes.Buffer
			for _, client := range clients {
				instructionPaths, err := setup.InstructionPaths(env, client.Name)
				if err != nil {
					t.Fatal(err)
				}
				for _, path := range instructionPaths {
					if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
						t.Fatal(err)
					}
					originalFiles[path] = []byte("Keep my " + filepath.Base(path) + " instructions.\nNo final newline")
					if err := os.WriteFile(path, originalFiles[path], 0600); err != nil {
						t.Fatal(err)
					}
					args := []string{"setup", client.Name, "--home", relayHome}
					if err := Run(context.Background(), args, &output, &output, "test"); err != nil {
						t.Fatalf("setup %s: %v: %s", client.Name, err, &output)
					}
				}
				skillPath := setup.SkillPath(env, client.Name)
				skillPaths = append(skillPaths, skillPath)
				notesPath := filepath.Join(filepath.Dir(skillPath), "notes.md")
				originalFiles[notesPath] = []byte("Keep my skill notes.")
				if err := os.WriteFile(notesPath, originalFiles[notesPath], 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(client.Path); err != nil {
					t.Fatal(err)
				}
			}

			for range 2 {
				output.Reset()
				args := []string{"setup", "--home", relayHome, "--remove", "--if-present"}
				if err := Run(context.Background(), args, &output, &output, "test"); err != nil {
					t.Fatalf("cleanup: %v: %s", err, &output)
				}
				if strings.Contains(output.String(), "not detected; skipped") || !strings.Contains(output.String(), "unload Relay tools") {
					t.Fatalf("unexpected cleanup output: %s", &output)
				}
			}
			for path, original := range originalFiles {
				data, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(data, original) {
					t.Fatalf("personal content changed at %s: %q: %v", path, data, err)
				}
			}
			for _, skillPath := range skillPaths {
				for _, path := range []string{skillPath, filepath.Join(filepath.Dir(skillPath), ".agent-relay-sha256")} {
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Fatalf("managed skill file retained at %s: %v", path, err)
					}
				}
			}
			for _, client := range clients {
				if _, err := os.Stat(client.Path); !os.IsNotExist(err) {
					t.Fatalf("removed client config recreated at %s: %v", client.Path, err)
				}
			}
		})
	}
}
