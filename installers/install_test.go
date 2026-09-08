package installers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type installerTest struct {
	root      string
	binDir    string
	relayHome string
	logPath   string
	env       []string
}

func newInstallerTest(t *testing.T) *installerTest {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mockDir := filepath.Join(root, "tools")
	if err := os.Mkdir(mockDir, 0700); err != nil {
		t.Fatal(err)
	}
	writeTool := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(mockDir, name), []byte("#!/bin/sh\nset -eu\n"+body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeTool("id", "echo 501\n")
	writeTool("uname", "if [ \"$1\" = -s ]; then echo \"${testOs:-Linux}\"; else echo \"${testArch:-x86_64}\"; fi\n")
	writeTool("tailscale", "[ \"${testDisconnected:-0}\" = 0 ] || exit 1\nif [ \"$1\" = status ]; then echo '{\"BackendState\":\"Running\"}'; else echo 100.64.0.1; fi\n")
	writeTool("launchctl", "exit ${testManagerFailure:-0}\n")
	writeTool("systemctl", "exit ${testManagerFailure:-0}\n")
	writeTool("curl", `
outputPath=''
requestUrl=''
quiet=0
while [ "$#" -gt 0 ]; do
    case "$1" in
        --output) outputPath=$2; shift 2 ;;
        --silent) quiet=1; shift ;;
        https://*) requestUrl=$1; shift ;;
        *) shift ;;
    esac
done
printf '%s\n' "$requestUrl" >> "$testRequests"
[ "${testDownloadFailure:-0}" = 0 ] || exit 22
case "$requestUrl" in
    */latest) printf '%s' 'https://github.com/echo1097/agent-relay/releases/tag/v0.1.0' ;;
    */SHA256SUMS) cp "$testManifest" "$outputPath" ;;
    */agent-relay_*)
        if [ "${testInterruptDownload:-0}" = 1 ]; then
            printf '%s\n' "$$" > "$testCurlPid"
            printf '%s\n' "$PPID" > "$testInstallerPid"
            mkfifo "$testBlock"
            exec 3<"$testBlock"
        fi
        if [ "$quiet" = 0 ]; then
            printf '  %% Total    %% Received %% Xferd  Average Speed   Time    Time     Time  Current\n                                 Dload  Upload   Total   Spent    Left  Speed\n' >&2
            printf '\r 50 16.0M 50 8.0M 0 0 1.0M 0 0:00:16 0:00:08 0:00:08 1.0M' >&2
        fi
        if [ "${testBinaryDownloadFailure:-0}" = 1 ]; then
            printf '\ncurl: (22) The requested URL returned error: 503\n' >&2
            exit 22
        fi
        cp "$testBinary" "$outputPath"
        if [ "$quiet" = 0 ]; then
            printf '\r100 16.0M 100 16.0M 0 0 1.0M 0 0:00:16 0:00:16 --:--:-- 1.0M\n' >&2
        fi
        ;;
    *) exit 22 ;;
esac
`)
	binaryData := []byte(`#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$testLog"
if [ "$1" = version ]; then echo 'agent-relay v0.1.0'; fi
if [ "$1" = status ]; then printf 'Node\n  Mac\n  node_test-123\n'; fi
if [ "$1" != version ]; then echo 'verbose diagnostic detail'; fi
if [ "$1" = "${testCommandFailure:-none}" ]; then echo 'specific failure reason' >&2; fi
[ "$1" != "${testCommandFailure:-none}" ] || exit 1
`)
	binaryPath := filepath.Join(root, "release-binary")
	if err := os.WriteFile(binaryPath, binaryData, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := ""
	for _, osName := range []string{"darwin", "linux"} {
		for _, cpuArch := range []string{"arm64", "amd64"} {
			manifest += fmt.Sprintf("%x  agent-relay_%s_%s\n", sha256.Sum256(binaryData), osName, cpuArch)
		}
	}
	manifestPath := filepath.Join(root, "SHA256SUMS")
	if err := os.WriteFile(manifestPath, []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	state := &installerTest{root: root, binDir: filepath.Join(root, "bin with spaces"), relayHome: filepath.Join(root, "relay home"), logPath: filepath.Join(root, "commands")}
	state.env = append(os.Environ(), "PATH="+mockDir+":"+os.Getenv("PATH"), "AGENT_RELAY_BIN_DIR="+state.binDir, "AGENT_RELAY_HOME="+state.relayHome, "AGENT_RELAY_SERVICE_NAME=installer-test", "AGENT_RELAY_VERSION=", "testBinary="+binaryPath, "testManifest="+manifestPath, "testLog="+state.logPath, "testRequests="+filepath.Join(root, "requests"), "testInstallerPid="+filepath.Join(root, "installer-pid"), "testCurlPid="+filepath.Join(root, "curl-pid"), "testBlock="+filepath.Join(root, "download-block"))
	return state
}

func (state *installerTest) run(t *testing.T, extraEnv []string, args ...string) (string, error) {
	t.Helper()
	command := exec.Command("sh", append([]string{"install.sh"}, args...)...)
	command.Env = append(append([]string{}, state.env...), extraEnv...)
	output, err := command.CombinedOutput()
	return string(output), err
}

func TestInstallerPlatformsAndUninstall(t *testing.T) {
	for _, target := range []struct{ osName, arch, asset string }{
		{"Darwin", "arm64", "darwin_arm64"}, {"Darwin", "x86_64", "darwin_amd64"},
		{"Linux", "aarch64", "linux_arm64"}, {"Linux", "x86_64", "linux_amd64"},
	} {
		t.Run(target.asset, func(t *testing.T) {
			state := newInstallerTest(t)
			extraEnv := []string{"testOs=" + target.osName, "testArch=" + target.arch}
			for iteration := 0; iteration < 2; iteration++ {
				output, err := state.run(t, extraEnv)
				if err != nil || !strings.Contains(output, "installed successfully") {
					t.Fatalf("%v: %s", err, output)
				}
			}
			requests, _ := os.ReadFile(filepath.Join(state.root, "requests"))
			if !strings.Contains(string(requests), "/v0.1.0/agent-relay_"+target.asset) {
				t.Fatal(string(requests))
			}
			commands, _ := os.ReadFile(state.logPath)
			for _, fragment := range []string{"status --home ", "service install --home ", "setup --home ", "--if-present", "doctor --home ", "--installation"} {
				if !strings.Contains(string(commands), fragment) {
					t.Fatal(string(commands))
				}
			}
			if err := os.MkdirAll(state.relayHome, 0700); err != nil {
				t.Fatal(err)
			}
			identityPath := filepath.Join(state.relayHome, "identity")
			if err := os.WriteFile(identityPath, []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
			for iteration := 0; iteration < 2; iteration++ {
				output, err := state.run(t, append(extraEnv, "testDisconnected=1", "testDownloadFailure=1"), "--uninstall")
				if err != nil {
					t.Fatalf("%v: %s", err, output)
				}
			}
			if _, err := os.Stat(filepath.Join(state.binDir, "agent-relay")); !os.IsNotExist(err) {
				t.Fatal("binary retained")
			}
			if data, err := os.ReadFile(identityPath); err != nil || string(data) != "preserve" {
				t.Fatal("identity removed")
			}
		})
	}
}

func TestInstallerPreflightAndDownloadFailures(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		env     []string
		message string
	}{
		{"unsupported-os", []string{"testOs=FreeBSD"}, "Unsupported OS"},
		{"unsupported-cpu", []string{"testArch=riscv64"}, "Unsupported CPU"},
		{"disconnected", []string{"testDisconnected=1"}, "Cannot query Tailscale"},
		{"manager", []string{"testManagerFailure=1"}, "No systemd user manager"},
		{"download", []string{"testDownloadFailure=1"}, "Cannot resolve the latest"},
		{"bad-tag", []string{"AGENT_RELAY_VERSION=v1/../bad"}, "Invalid release tag"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			state := newInstallerTest(t)
			output, err := state.run(t, testCase.env)
			if err == nil || !strings.Contains(output, testCase.message) {
				t.Fatalf("%v: %s", err, output)
			}
			if _, err := os.Stat(state.logPath); !os.IsNotExist(err) {
				t.Fatal("executed unverified binary")
			}
			if _, err := os.Stat(filepath.Join(state.binDir, "agent-relay")); !os.IsNotExist(err) {
				t.Fatal("installed binary after failure")
			}
		})
	}
}

func TestInstallerChecksumFailures(t *testing.T) {
	for _, manifest := range []string{"", strings.Repeat("0", 64) + "  agent-relay_linux_amd64\n", strings.Repeat("0", 64) + "  agent-relay_linux_amd64\n" + strings.Repeat("0", 64) + "  agent-relay_linux_amd64\n"} {
		state := newInstallerTest(t)
		if err := os.WriteFile(filepath.Join(state.root, "SHA256SUMS"), []byte(manifest), 0600); err != nil {
			t.Fatal(err)
		}
		output, err := state.run(t, nil)
		if err == nil {
			t.Fatal(output)
		}
		if _, err := os.Stat(state.logPath); !os.IsNotExist(err) {
			t.Fatal("executed before verification")
		}
	}
}

func TestInstallerRefusesUnmanagedAndChangedFiles(t *testing.T) {
	for _, kind := range []string{"unmanaged", "symlink", "changed", "home"} {
		t.Run(kind, func(t *testing.T) {
			state := newInstallerTest(t)
			binaryPath := filepath.Join(state.binDir, "agent-relay")
			if err := os.MkdirAll(state.binDir, 0700); err != nil {
				t.Fatal(err)
			}
			if kind == "changed" || kind == "home" {
				if output, err := state.run(t, nil); err != nil {
					t.Fatalf("%v: %s", err, output)
				}
			}
			if kind == "symlink" {
				if err := os.Symlink(filepath.Join(state.root, "release-binary"), binaryPath); err != nil {
					t.Fatal(err)
				}
			} else if kind != "home" {
				if err := os.WriteFile(binaryPath, []byte("unrelated"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			extraEnv := []string{}
			if kind == "home" {
				extraEnv = append(extraEnv, "AGENT_RELAY_HOME="+filepath.Join(state.root, "different"))
			}
			before, _ := os.ReadFile(binaryPath)
			output, err := state.run(t, extraEnv)
			if err == nil {
				t.Fatal(output)
			}
			after, _ := os.ReadFile(binaryPath)
			if string(before) != string(after) {
				t.Fatal("changed conflicting file")
			}
		})
	}
}

func TestInstallerRetainsRecoveryOnSetupFailure(t *testing.T) {
	for _, commandName := range []string{"status", "service", "setup", "doctor"} {
		t.Run(commandName, func(t *testing.T) {
			state := newInstallerTest(t)
			output, err := state.run(t, []string{"testCommandFailure=" + commandName})
			if err == nil || strings.Contains(output, "installed successfully") {
				t.Fatalf("%v: %s", err, output)
			}
			if _, err := os.Stat(filepath.Join(state.binDir, "agent-relay")); err != nil {
				t.Fatal("recovery binary missing")
			}
			if output, err := state.run(t, nil); err != nil {
				t.Fatalf("retry failed: %v: %s", err, output)
			}
		})
	}
}

func TestInstallerConciseOutput(t *testing.T) {
	state := newInstallerTest(t)
	output, err := state.run(t, []string{"NO_COLOR=1", "TERM=xterm-256color"})
	if err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	if strings.Contains(output, "verbose diagnostic detail") || strings.Contains(output, "\x1b[") || strings.Contains(output, "\r") || strings.Contains(output, "ETA") {
		t.Fatalf("unexpected noise: %s", output)
	}
	if !strings.Contains(output, "Devices on your tailnet pair automatically") || !strings.HasSuffix(output, "node_test-123\n") {
		t.Fatalf("missing pairing details: %s", output)
	}
	output, err = state.run(t, []string{"testCommandFailure=doctor"})
	if err == nil || !strings.Contains(output, "specific failure reason") || !strings.Contains(output, "verbose diagnostic detail") {
		t.Fatalf("failure diagnostics missing: %v: %s", err, output)
	}
}

func TestInstallerTerminalDownloadProgress(t *testing.T) {
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("terminal recorder is unavailable")
	}
	for _, fails := range []bool{false, true} {
		t.Run(fmt.Sprintf("failure=%t", fails), func(t *testing.T) {
			state := newInstallerTest(t)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			var command *exec.Cmd
			switch runtime.GOOS {
			case "darwin":
				command = exec.CommandContext(ctx, "script", "-q", "/dev/null", "sh", "install.sh")
			case "linux":
				command = exec.CommandContext(ctx, "script", "-q", "-e", "-c", "sh install.sh", "/dev/null")
			default:
				t.Skip("terminal recorder flags are unavailable for this platform")
			}
			command.Env = append(append([]string{}, state.env...), "TERM=xterm-256color", "NO_COLOR=1")
			if fails {
				command.Env = append(command.Env, "testBinaryDownloadFailure=1")
			}
			data, err := command.CombinedOutput()
			output := string(data)
			if !strings.Contains(output, "[========--------]  50%  8.0 MiB / 16.0 MiB  ETA 0:00:08") || strings.Contains(output, "% Total") || strings.Contains(output, "Dload") {
				t.Fatalf("terminal progress: %v: %q", err, output)
			}
			if fails {
				if err == nil || !strings.Contains(output, "curl: (22)") || !strings.Contains(output, "Download failed:") || strings.Contains(output, "installed successfully") {
					t.Fatalf("download failure was hidden: %v: %q", err, output)
				}
				if _, err := os.Stat(state.logPath); !os.IsNotExist(err) {
					t.Fatal("executed a failed download")
				}
			} else if err != nil || !strings.Contains(output, "[================] 100%") || !strings.Contains(output, "installed successfully") {
				t.Fatalf("completed download: %v: %q", err, output)
			}
			if _, err := os.Stat(filepath.Join(state.binDir, ".agent-relay-install-lock")); !os.IsNotExist(err) {
				t.Fatal("installer lock was not cleaned up")
			}
		})
	}
}

func TestDownloadProgressUnknownSizeAndWarnings(t *testing.T) {
	data, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := strings.TrimSuffix(string(data), "main \"$@\"\n") + "downloadProgress\n"
	command := exec.Command("sh", "-c", script)
	command.Stdin = strings.NewReader("\r0 0 0 512k 0 0 512k 0 --:--:-- 0:00:01 --:--:-- 512k\nWarning: Retrying download.\n\r0 0 0 1.0M 0 0 512k 0 --:--:-- 0:00:02 --:--:-- 512k\n")
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "512 KiB / ?  ETA --") || !strings.Contains(string(output), "1.0 MiB / ?  ETA --") || !strings.Contains(string(output), "\nWarning: Retrying download.\n") {
		t.Fatalf("unknown size and retry warning: %v: %q", err, output)
	}
	if strings.Contains(string(output), "100%") || !strings.HasSuffix(string(output), "\n") {
		t.Fatalf("misleading completion or unfinished line: %q", output)
	}
}

func TestInstallerInterruptCleansUpDownloadProcesses(t *testing.T) {
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("terminal recorder is unavailable")
	}
	state := newInstallerTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.CommandContext(ctx, "script", "-q", "/dev/null", "sh", "install.sh")
	case "linux":
		command = exec.CommandContext(ctx, "script", "-q", "-e", "-c", "sh install.sh", "/dev/null")
	default:
		t.Skip("terminal recorder flags are unavailable for this platform")
	}
	command.Env = append(append([]string{}, state.env...), "TERM=xterm-256color", "NO_COLOR=1", "testInterruptDownload=1")
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waitResult := make(chan error, 1)
	go func() { waitResult <- command.Wait() }()
	waitReturned := false
	defer func() {
		if !waitReturned {
			_ = command.Process.Kill()
			<-waitResult
		}
	}()

	var pidData []byte
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var err error
		pidData, err = os.ReadFile(filepath.Join(state.root, "installer-pid"))
		if err == nil && strings.TrimSpace(string(pidData)) != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(pidData) == 0 {
		t.Fatal("download did not start")
	}
	installerPid, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
	if err != nil {
		t.Fatal(err)
	}
	installerProcess, err := os.FindProcess(installerPid)
	if err != nil {
		t.Fatal(err)
	}
	if err := installerProcess.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}

	select {
	case err = <-waitResult:
		waitReturned = true
	case <-time.After(5 * time.Second):
		t.Fatal("installer did not exit after TERM")
	}
	if err == nil || strings.Contains(output.String(), "installed successfully") {
		t.Fatalf("interrupt was not reported: %v: %q", err, output.String())
	}
	if _, err := os.Stat(filepath.Join(state.binDir, ".agent-relay-install-lock")); !os.IsNotExist(err) {
		t.Fatal("installer lock was not cleaned up")
	}
	if entries, err := filepath.Glob(filepath.Join(state.binDir, ".agent-relay-download.*")); err != nil || len(entries) != 0 {
		t.Fatalf("download tempdir was not cleaned up: %v: %v", err, entries)
	}
	curlPidData, err := os.ReadFile(filepath.Join(state.root, "curl-pid"))
	if err != nil {
		t.Fatal(err)
	}
	curlPid, err := strconv.Atoi(strings.TrimSpace(string(curlPidData)))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(curlPid, 0); err == nil || !errors.Is(err, syscall.ESRCH) {
		t.Fatal("curl process survived interruption")
	}
}
