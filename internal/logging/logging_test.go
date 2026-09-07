package logging

import (
	"bytes"
	"strings"
	"testing"
)

func TestLevelAndStructuredFields(t *testing.T) {
	var output bytes.Buffer
	logger, err := New(&output, "info")
	if err != nil {
		t.Fatal(err)
	}
	logger.Debug("hidden")
	logger.Info("daemon started", "pid", 42)
	if strings.Contains(output.String(), "hidden") || !strings.Contains(output.String(), "pid=42") || !strings.Contains(output.String(), "level=INFO") {
		t.Fatalf("unexpected log: %s", output.String())
	}
	if _, err := New(&output, "invalid"); err == nil {
		t.Fatal("accepted invalid level")
	}
}
