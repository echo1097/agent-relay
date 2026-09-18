package cli

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

func TestMaintenanceHelpAndInvalidArguments(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	for _, action := range []string{"update", "uninstall"} {
		for _, helpFlag := range []string{"help", "--help", "-h"} {
			var output bytes.Buffer
			if err := Run(context.Background(), []string{action, helpFlag}, &output, &output, "test"); err != nil || !strings.Contains(output.String(), "Usage: agent-relay "+action) {
				t.Fatalf("help %s: %v: %s", action, err, &output)
			}
		}
		for _, args := range [][]string{{"extra"}, {"help", "extra"}, {"--unknown"}, {"--home", root}} {
			var output bytes.Buffer
			if err := Run(context.Background(), append([]string{action}, args...), &output, &output, "test"); err == nil {
				t.Fatalf("accepted invalid %s arguments: %v", action, args)
			}
		}
	}
	for _, args := range [][]string{{"update", "--version", "../bad"}, {"uninstall", "--version", "v0.1.0"}} {
		var output bytes.Buffer
		if err := Run(context.Background(), args, &output, &output, "test"); err == nil {
			t.Fatalf("accepted invalid release arguments: %v", args)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("help or invalid arguments created local state", err)
	}
}
