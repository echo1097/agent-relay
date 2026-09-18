package installers

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode"

	"agent-relay/internal/fileedit"
)

func Run(ctx context.Context, action, binaryPath, releaseVersion string, output, errorOutput io.Writer) error {
	if action != "update" && action != "uninstall" {
		return fmt.Errorf("unsupported installer action %q", action)
	}
	if releaseVersion != "" {
		if action != "update" {
			return errors.New("only update accepts a release version")
		}
		if matched, _ := regexp.MatchString(`^v[0-9][a-zA-Z0-9._-]*$`, releaseVersion); !matched {
			return errors.New("release version must be a tag such as v0.1.0")
		}
	}
	if !filepath.IsAbs(binaryPath) || filepath.Base(binaryPath) != "agent-relay" {
		return errors.New("run this command from the installer-managed agent-relay executable")
	}
	binDir := filepath.Dir(binaryPath)
	receiptPath := filepath.Join(binDir, ".agent-relay-receipt")
	data, _, err := fileedit.Read(receiptPath)
	if err != nil {
		return fmt.Errorf("read installation receipt: %w", err)
	}
	if data == nil {
		return fmt.Errorf("no installation receipt at %s; update and uninstall require an installer-managed executable", receiptPath)
	}
	fields := strings.Split(string(data), "\n")
	if len(fields) != 4 || fields[3] != "" {
		return errors.New("invalid installation receipt; review it before retrying")
	}
	checksum, err := hex.DecodeString(fields[0])
	if err != nil || len(checksum) != 32 || fields[0] != strings.ToLower(fields[0]) {
		return errors.New("invalid checksum in installation receipt")
	}
	relayHome, serviceName := fields[1], fields[2]
	if !filepath.IsAbs(relayHome) || strings.IndexFunc(relayHome, unicode.IsControl) >= 0 {
		return errors.New("invalid Relay home in installation receipt")
	}
	if matched, _ := regexp.MatchString(`^[a-z][a-z0-9-]{0,47}$`, serviceName); !matched {
		return errors.New("invalid service name in installation receipt")
	}

	args := []string{"-c", installerScript, "agent-relay " + action}
	if action == "uninstall" {
		args = append(args, "--uninstall")
	}
	command := exec.CommandContext(ctx, "sh", args...)
	command.Env = append(os.Environ(),
		"AGENT_RELAY_BIN_DIR="+binDir,
		"AGENT_RELAY_HOME="+relayHome,
		"AGENT_RELAY_SERVICE_NAME="+serviceName,
		"AGENT_RELAY_VERSION="+releaseVersion,
	)
	command.Stdout = output
	command.Stderr = errorOutput
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error { return syscall.Kill(-command.Process.Pid, syscall.SIGTERM) }
	command.WaitDelay = 5 * time.Second
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%s failed: %w", action, err)
	}
	return nil
}
