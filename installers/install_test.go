package installers

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
while [ "$#" -gt 0 ]; do
    case "$1" in
        --output) outputPath=$2; shift 2 ;;
        https://*) requestUrl=$1; shift ;;
        *) shift ;;
    esac
done
printf '%s\n' "$requestUrl" >> "$testRequests"
[ "${testDownloadFailure:-0}" = 0 ] || exit 22
case "$requestUrl" in
    */latest) printf '%s' 'https://github.com/echo1097/agent-relay/releases/tag/v0.1.0' ;;
    */SHA256SUMS) cp "$testManifest" "$outputPath" ;;
    */agent-relay_*) cp "$testBinary" "$outputPath" ;;
    *) exit 22 ;;
esac
`)
	binaryData := []byte(`#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$testLog"
if [ "$1" = version ]; then echo 'agent-relay v0.1.0'; fi
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
	state.env = append(os.Environ(), "PATH="+mockDir+":"+os.Getenv("PATH"), "AGENT_RELAY_BIN_DIR="+state.binDir, "AGENT_RELAY_HOME="+state.relayHome, "AGENT_RELAY_SERVICE_NAME=installer-test", "AGENT_RELAY_VERSION=", "testBinary="+binaryPath, "testManifest="+manifestPath, "testLog="+state.logPath, "testRequests="+filepath.Join(root, "requests"))
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
