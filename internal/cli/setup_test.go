package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupCLI(t *testing.T) {
	root := t.TempDir()
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
	for _, badArgs := range [][]string{{"setup", "wrong"}, {"setup", "--config", configPath}, {"setup", "codex", "extra"}} {
		if err := Run(context.Background(), badArgs, &output, &output, "test"); err == nil {
			t.Fatal(badArgs)
		}
	}
}
