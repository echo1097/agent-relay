package setup

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestInstructionPathUsesConfiguredLocations(t *testing.T) {
	root := t.TempDir()
	codexHome := filepath.Join(root, "codex")
	claudeHome := filepath.Join(root, "claude")
	if err := os.MkdirAll(codexHome, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(claudeHome, 0700); err != nil {
		t.Fatal(err)
	}

	getenv := func(key string) string {
		return map[string]string{
			"CODEX_HOME":        codexHome,
			"CLAUDE_CONFIG_DIR": claudeHome,
		}[key]
	}
	env := Environment{Home: root, Getenv: getenv}

	path, err := InstructionPath(env, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(codexHome, "AGENTS.md") {
		t.Fatalf("codex path: %s", path)
	}

	path, err = InstructionPath(env, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(claudeHome, "CLAUDE.md") {
		t.Fatalf("claude path: %s", path)
	}

	overridePath := filepath.Join(codexHome, "AGENTS.override.md")
	if err := os.WriteFile(overridePath, []byte("use this"), 0600); err != nil {
		t.Fatal(err)
	}
	path, err = InstructionPath(env, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if path != overridePath {
		t.Fatalf("codex override path: %s", path)
	}

	if err := os.WriteFile(overridePath, nil, 0600); err != nil {
		t.Fatal(err)
	}
	path, err = InstructionPath(env, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(codexHome, "AGENTS.md") {
		t.Fatalf("empty override path: %s", path)
	}

	defaultEnv := Environment{Home: root, Getenv: func(string) string { return "" }}
	path, err = InstructionPath(defaultEnv, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(root, ".codex", "AGENTS.md") {
		t.Fatalf("default codex path: %s", path)
	}
	path, err = InstructionPath(defaultEnv, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(root, ".claude", "CLAUDE.md") {
		t.Fatalf("default claude path: %s", path)
	}

	if _, err := InstructionPath(env, "other"); err == nil {
		t.Fatal("accepted unsupported client")
	}
}

func TestSyncInstructionsPreservesBytesAndRemovesOnlyManagedBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	original := []byte("user instructions\nwith no final newline")
	if err := os.WriteFile(path, original, 0640); err != nil {
		t.Fatal(err)
	}

	result, err := SyncInstructions(path, "/skills/first", false)
	if err != nil || !result.Changed || result.Backup == "" {
		t.Fatalf("add result: %+v %v", result, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(instructionBlock("/skills/first"))) {
		t.Fatal("managed block was not added")
	}

	result, err = SyncInstructions(path, "/skills/first", false)
	if err != nil || result.Changed || result.Backup != "" {
		t.Fatalf("repeat result: %+v %v", result, err)
	}
	backups, err := filepath.Glob(path + ".agent-relay-backup-*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups: %v %v", backups, err)
	}

	result, err = SyncInstructions(path, "/skills/second", false)
	if err != nil || !result.Changed || result.Backup == "" {
		t.Fatalf("update result: %+v %v", result, err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(instructionBlock("/skills/first"))) || !bytes.Contains(data, []byte(instructionBlock("/skills/second"))) {
		t.Fatal("managed block was not updated")
	}

	suffix := []byte("\nUser guidance added after setup.\n")
	if err := os.WriteFile(path, append(data, suffix...), 0640); err != nil {
		t.Fatal(err)
	}
	original = append(original, suffix...)

	result, err = SyncInstructions(path, "/skills/second", true)
	if err != nil || !result.Changed || result.Backup == "" {
		t.Fatalf("remove result: %+v %v", result, err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, original) {
		t.Fatalf("user bytes changed: %q", data)
	}
}

func TestSyncInstructionsHandlesEmptyAndAbsentFiles(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		data   []byte
		create bool
	}{
		{name: "empty", create: true, data: []byte{}},
		{name: "absent"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "CLAUDE.md")
			if testCase.create {
				if err := os.WriteFile(path, testCase.data, 0600); err != nil {
					t.Fatal(err)
				}
			}

			result, err := SyncInstructions(path, "/skills/startup", false)
			if err != nil || !result.Changed {
				t.Fatalf("add result: %+v %v", result, err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(data, []byte(instructionBlock("/skills/startup"))) {
				t.Fatal("managed block was not added")
			}

			result, err = SyncInstructions(path, "/skills/startup", true)
			if err != nil || !result.Changed {
				t.Fatalf("remove result: %+v %v", result, err)
			}
			data, err = os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(data) != 0 {
				t.Fatalf("file was not emptied: %q", data)
			}
		})
	}
}

func TestSyncInstructionsRejectsMalformedAndDuplicateMarkers(t *testing.T) {
	blocks := []struct {
		name string
		data string
	}{
		{name: "start without end", data: "before\n" + instructionStart + "\nafter"},
		{name: "end without start", data: "before\n" + instructionEnd + "\nafter"},
		{name: "duplicate block", data: instructionBlock("/skills/one") + "\n" + instructionBlock("/skills/two")},
		{name: "inline markers", data: "user example " + instructionBlock("/skills/one")},
		{name: "reversed markers", data: instructionEnd + "\ntext\n" + instructionStart},
	}
	for _, testCase := range blocks {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "AGENTS.md")
			original := []byte(testCase.data)
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			result, err := SyncInstructions(path, "/skills/new", false)
			if err == nil || result.Changed || result.Backup != "" {
				t.Fatalf("accepted malformed markers: %+v %v", result, err)
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !bytes.Equal(data, original) {
				t.Fatal("malformed file changed")
			}
		})
	}
}

func TestSyncInstructionsRejectsSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "AGENTS.md")
	original := []byte("keep this target")
	if err := os.WriteFile(target, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	result, err := SyncInstructions(link, "/skills/startup", false)
	if err == nil || result.Changed || result.Backup != "" {
		t.Fatalf("accepted symlink: %+v %v", result, err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, original) {
		t.Fatal("symlink target changed")
	}
	linkData, err := os.ReadFile(link)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(linkData, original) {
		t.Fatal("symlink content changed")
	}
}
