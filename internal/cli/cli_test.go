package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommands(t *testing.T) {
	home := filepath.Join(t.TempDir(), "relay")
	for _, args := range [][]string{{"version"}, {"help"}} {
		var output bytes.Buffer
		if err := Run(context.Background(), args, &output, &output, "test-version"); err != nil {
			t.Fatal(err)
		}
		if output.Len() == 0 {
			t.Fatal("empty output")
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("unexpected application directory")
	}
	var output bytes.Buffer
	if err := Run(context.Background(), []string{"status", "--home", home}, &output, &output, "test-version"); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Stopped", "node_", "Schema version: 2", "test-version"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("missing %q in %s", expected, output.String())
		}
	}
	for _, args := range [][]string{{"peers"}, {"status", "extra"}, {"version", "extra"}, {"status", "--unknown"}} {
		if err := Run(context.Background(), args, &output, &output, "test"); err == nil {
			t.Fatalf("accepted invalid args: %v", args)
		}
	}
}

func TestAgentCommands(t *testing.T) {
	home := t.TempDir()
	run := func(args ...string) (string, error) {
		t.Helper()
		var output, errorOutput bytes.Buffer
		commandArgs := append([]string{"agents"}, args...)
		commandArgs = append(commandArgs, "--home", home)
		err := Run(context.Background(), commandArgs, &output, &errorOutput, "test")
		return output.String(), err
	}
	output, err := run("list")
	if err != nil || !strings.Contains(output, "No local agents") {
		t.Fatalf("empty list: %q, %v", output, err)
	}
	output, err = run("register", "--name", "cli-agent", "--task", "test", "--file", "a.go", "--file", "b.go")
	if err != nil {
		t.Fatal(err)
	}
	agentID := strings.TrimSpace(output)
	if !strings.HasPrefix(agentID, "agent_") {
		t.Fatalf("registration output: %q", output)
	}
	for _, args := range [][]string{
		{"get", "--id", agentID},
		{"set-status", "--id", agentID, "--state", "busy"},
		{"heartbeat", "--id", agentID},
		{"update-metadata", "--id", agentID, "--task", "changed"},
	} {
		output, err := run(args...)
		if err != nil || !strings.Contains(output, agentID) {
			t.Fatalf("command %v: %q, %v", args, output, err)
		}
	}
	output, err = run("list")
	if err != nil || !strings.Contains(output, "busy") || !strings.Contains(output, "changed") {
		t.Fatalf("list: %q, %v", output, err)
	}
	if _, err := run("disconnect", "--id", agentID); err != nil {
		t.Fatal(err)
	}
	output, err = run("get", "--id", agentID)
	if err != nil || !strings.Contains(output, `"status": "offline"`) {
		t.Fatalf("disconnect: %q, %v", output, err)
	}
	for _, args := range [][]string{{"get"}, {"register"}, {"heartbeat", "--id", "unknown"}, {"set-status", "--id", agentID, "--state", "wrong"}, {"list", "--task", "ignored"}} {
		if _, err := run(args...); err == nil {
			t.Fatalf("accepted invalid args: %v", args)
		}
	}
}
