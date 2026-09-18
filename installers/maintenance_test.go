package installers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestBundledInstallerMatchesScript(t *testing.T) {
	data, err := os.ReadFile("install.sh")
	if err != nil || string(data) != installerScript {
		t.Fatal("update script_content.go to match install.sh", err)
	}
}

func prepareMaintenanceTest(t *testing.T) *installerTest {
	t.Helper()
	state := newInstallerTest(t)
	if output, err := state.run(t, nil); err != nil {
		t.Fatalf("install fixture: %v: %s", err, output)
	}
	for _, entry := range state.env {
		key, value, found := strings.Cut(entry, "=")
		if found && (key == "PATH" || strings.HasPrefix(key, "test") || strings.HasPrefix(key, "AGENT_RELAY_")) {
			t.Setenv(key, value)
		}
	}
	t.Setenv("HOME", state.root)
	t.Setenv("AGENT_RELAY_BIN_DIR", filepath.Join(state.root, "wrong-bin"))
	t.Setenv("AGENT_RELAY_HOME", filepath.Join(state.root, "wrong-home"))
	t.Setenv("AGENT_RELAY_SERVICE_NAME", "wrong-service")
	t.Setenv("AGENT_RELAY_VERSION", "v0.0.1")
	for _, path := range []string{state.logPath, filepath.Join(state.root, "requests")} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(state.relayHome, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state.relayHome, "identity"), []byte("keep identity"), 0600); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestManagedUninstallUsesRecordedSettingsOffline(t *testing.T) {
	state := prepareMaintenanceTest(t)
	t.Setenv("testDisconnected", "1")
	t.Setenv("testDownloadFailure", "1")
	var output bytes.Buffer
	binaryPath := filepath.Join(state.binDir, "agent-relay")
	if err := Run(context.Background(), "uninstall", binaryPath, "", &output, &output); err != nil {
		t.Fatalf("uninstall: %v: %s", err, &output)
	}
	for _, path := range []string{binaryPath, filepath.Join(state.binDir, ".agent-relay-receipt")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("managed file retained: %s: %v", path, err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(state.relayHome, "identity")); err != nil || string(data) != "keep identity" {
		t.Fatal("identity was not preserved", err)
	}
	if _, err := os.Stat(filepath.Join(state.root, "requests")); !os.IsNotExist(err) {
		t.Fatal("uninstall attempted a download", err)
	}
	commands, err := os.ReadFile(state.logPath)
	expected := "setup --home " + state.relayHome + " --remove --if-present\nservice uninstall --name installer-test\n"
	if err != nil || string(commands) != expected {
		t.Fatalf("wrong cleanup commands: %q: %v", commands, err)
	}
}

func TestManagedUpdateUsesLatestOrSelectedRelease(t *testing.T) {
	for _, releaseVersion := range []string{"", "v0.3.0"} {
		name := "latest"
		if releaseVersion != "" {
			name = "selected"
		}
		t.Run(name, func(t *testing.T) {
			state := prepareMaintenanceTest(t)
			t.Setenv("testLatestVersion", "v0.2.0")
			wantVersion := releaseVersion
			if wantVersion == "" {
				wantVersion = "v0.2.0"
			}
			releasePath := filepath.Join(state.root, "release-binary")
			data, err := os.ReadFile(releasePath)
			if err != nil {
				t.Fatal(err)
			}
			data = bytes.ReplaceAll(data, []byte("v0.1.0"), []byte(wantVersion))
			if err := os.WriteFile(releasePath, data, 0700); err != nil {
				t.Fatal(err)
			}
			manifest := fmt.Sprintf("%x  agent-relay_linux_amd64\n", sha256.Sum256(data))
			if err := os.WriteFile(filepath.Join(state.root, "SHA256SUMS"), []byte(manifest), 0600); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			binaryPath := filepath.Join(state.binDir, "agent-relay")
			if err := Run(context.Background(), "update", binaryPath, releaseVersion, &output, &output); err != nil {
				t.Fatalf("update: %v: %s", err, &output)
			}
			installed, err := os.ReadFile(binaryPath)
			if err != nil || !bytes.Equal(installed, data) {
				t.Fatal("updated binary was not installed", err)
			}
			receipt, err := os.ReadFile(filepath.Join(state.binDir, ".agent-relay-receipt"))
			expectedReceipt := fmt.Sprintf("%x\n%s\ninstaller-test\n", sha256.Sum256(data), state.relayHome)
			if err != nil || string(receipt) != expectedReceipt {
				t.Fatalf("wrong updated receipt: %q: %v", receipt, err)
			}
			requests, err := os.ReadFile(filepath.Join(state.root, "requests"))
			if err != nil || !strings.Contains(string(requests), "/"+wantVersion+"/agent-relay_linux_amd64") || strings.Contains(string(requests), "/latest") != (releaseVersion == "") {
				t.Fatalf("wrong release requested: %q: %v", requests, err)
			}
			commands, err := os.ReadFile(state.logPath)
			if err != nil || !strings.Contains(string(commands), "service install --home "+state.relayHome+" --name installer-test\n") || !strings.Contains(string(commands), "setup --home "+state.relayHome+" --if-present\n") {
				t.Fatalf("service/client update missing: %q: %v", commands, err)
			}
			if identity, err := os.ReadFile(filepath.Join(state.relayHome, "identity")); err != nil || string(identity) != "keep identity" {
				t.Fatal("update changed identity", err)
			}
		})
	}
}

func TestManagedCommandsRefuseInvalidInstallation(t *testing.T) {
	for _, action := range []string{"update", "uninstall"} {
		for _, kind := range []string{"missing receipt", "malformed receipt", "invalid hash", "relative home", "invalid service", "changed binary", "symlink binary", "symlink receipt"} {
			t.Run(action+"/"+kind, func(t *testing.T) {
				state := prepareMaintenanceTest(t)
				binaryPath := filepath.Join(state.binDir, "agent-relay")
				receiptPath := filepath.Join(state.binDir, ".agent-relay-receipt")
				data, err := os.ReadFile(receiptPath)
				if err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "missing receipt":
					err = os.Remove(receiptPath)
				case "malformed receipt":
					err = os.WriteFile(receiptPath, []byte("broken\n"), 0600)
				case "invalid hash":
					err = os.WriteFile(receiptPath, []byte("nohash\n"+state.relayHome+"\ninstaller-test\n"), 0600)
				case "relative home":
					err = os.WriteFile(receiptPath, bytes.Replace(data, []byte(state.relayHome), []byte("relative/home"), 1), 0600)
				case "invalid service":
					err = os.WriteFile(receiptPath, bytes.Replace(data, []byte("installer-test"), []byte("invalid/service"), 1), 0600)
				case "changed binary":
					err = os.WriteFile(binaryPath, []byte("unmanaged binary"), 0700)
				case "symlink binary", "symlink receipt":
					path := binaryPath
					if kind == "symlink receipt" {
						path = receiptPath
					}
					if err := os.Rename(path, path+".original"); err != nil {
						t.Fatal(err)
					}
					err = os.Symlink(path+".original", path)
				}
				if err != nil {
					t.Fatal(err)
				}
				var output bytes.Buffer
				if err := Run(context.Background(), action, binaryPath, "", &output, &output); err == nil {
					t.Fatal("accepted invalid installation", &output)
				}
				if _, err := os.Lstat(binaryPath); err != nil {
					t.Fatal("refusal removed executable", err)
				}
				for _, path := range []string{state.logPath, filepath.Join(state.root, "requests")} {
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Fatalf("executed a binary or download after refusal: %s: %v", path, err)
					}
				}
			})
		}
	}
}

func TestManagedUpdateCancellationCleansUp(t *testing.T) {
	for _, stage := range []struct{ name, key, value, pidFile string }{
		{"download", "testInterruptDownload", "1", "curl-pid"},
		{"release lookup", "testInterruptResolve", "1", "curl-pid"},
		{"client setup", "testInterruptCommand", "setup", "step-pid"},
	} {
		t.Run(stage.name, func(t *testing.T) {
			checkManagedUpdateCancellation(t, stage.key, stage.value, stage.pidFile)
		})
	}
}

func checkManagedUpdateCancellation(t *testing.T, key, value, pidFile string) {
	t.Helper()
	state := prepareMaintenanceTest(t)
	t.Setenv(key, value)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var output bytes.Buffer
	result := make(chan error, 1)
	go func() {
		result <- Run(ctx, "update", filepath.Join(state.binDir, "agent-relay"), "", &output, &output)
	}()
	deadline := time.Now().Add(5 * time.Second)
	started := false
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(filepath.Join(state.root, "installer-pid"))
		if err == nil && len(data) > 0 {
			started = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	err := <-result
	pidData, readErr := os.ReadFile(filepath.Join(state.root, pidFile))
	if readErr != nil {
		t.Fatal(readErr)
	}
	childPid, parseErr := strconv.Atoi(strings.TrimSpace(string(pidData)))
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	if processErr := syscall.Kill(childPid, 0); !errors.Is(processErr, syscall.ESRCH) {
		_ = syscall.Kill(childPid, syscall.SIGKILL)
		t.Errorf("child process survived update cancellation: %v", processErr)
	}
	if !started || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: started=%t: %v: %s", started, err, &output)
	}
	if _, err := os.Stat(filepath.Join(state.binDir, ".agent-relay-install-lock")); !os.IsNotExist(err) {
		t.Fatal("cancelled update retained installer lock", err)
	}
	paths, err := filepath.Glob(filepath.Join(state.binDir, ".agent-relay-download.*"))
	if err != nil || len(paths) != 0 {
		t.Fatalf("cancelled update retained temporary files: %v: %v", paths, err)
	}
}
