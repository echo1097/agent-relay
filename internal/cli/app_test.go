package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppHelp(t *testing.T) {
	var output bytes.Buffer
	if err := runApp([]string{"help"}, &output, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "agent-relay app") {
		t.Fatalf("unexpected help: %s", output.String())
	}
	if err := runApp([]string{"unexpected"}, &output, &output); err == nil {
		t.Fatal("expected invalid arguments to fail")
	}
}

func TestAppMissingDependenciesAndBuild(t *testing.T) {
	appDir := t.TempDir()
	t.Setenv("AGENT_RELAY_APP_DIR", appDir)
	var output bytes.Buffer
	err := runApp(nil, &output, &output)
	if err == nil || !strings.Contains(err.Error(), "npm install") {
		t.Fatalf("expected dependency setup instruction, got %v", err)
	}
	electronName := "electron"
	if os.PathSeparator == '\\' {
		electronName += ".cmd"
	}
	binDir := filepath.Join(appDir, "node_modules", ".bin")
	if err := os.MkdirAll(binDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, electronName), nil, 0600); err != nil {
		t.Fatal(err)
	}
	err = runApp(nil, &output, &output)
	if err == nil || !strings.Contains(err.Error(), "npm run build") {
		t.Fatalf("expected build instruction, got %v", err)
	}
}

func TestFindAppDirBesideCheckout(t *testing.T) {
	workDir := t.TempDir()
	appDir := filepath.Join(workDir, "desktop")
	if err := os.MkdirAll(filepath.Join(appDir, "electron"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "electron", "main.cjs"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_RELAY_APP_DIR", "")
	t.Chdir(workDir)
	result, err := findAppDir()
	if err != nil {
		t.Fatal(err)
	}
	resolvedResult, err := filepath.EvalSymlinks(result)
	if err != nil {
		t.Fatal(err)
	}
	resolvedExpected, err := filepath.EvalSymlinks(appDir)
	if err != nil {
		t.Fatal(err)
	}
	if resolvedResult != resolvedExpected {
		t.Fatalf("expected %s, got %s", resolvedExpected, resolvedResult)
	}
}
