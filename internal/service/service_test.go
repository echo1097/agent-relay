package service

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeManager struct {
	manager *Manager
	calls   []string
	loaded  bool
	running bool
	enabled bool
	fail    string
}

func newFake(t *testing.T, platform string) *fakeManager {
	t.Helper()
	fake := &fakeManager{}
	fake.manager = &Manager{Platform: platform, UserHome: t.TempDir(), UserID: 501, Name: "relay-test", Running: func(string) (bool, error) { return fake.running, nil }}
	fake.manager.Ready = func(context.Context, string) error { return nil }
	fake.manager.Run = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		call := command + " " + strings.Join(args, " ")
		fake.calls = append(fake.calls, call)
		if fake.fail != "" && strings.Contains(call, fake.fail) {
			return []byte("permission denied"), errors.New("command failed")
		}
		if platform == "darwin" {
			switch args[0] {
			case "print":
				if args[1] == "gui/501" {
					return []byte("domain"), nil
				}
				if !fake.loaded {
					return []byte("Could not find service"), errors.New("exit 113")
				}
				if fake.running {
					return []byte("state = running"), nil
				}
				return []byte("state = waiting"), nil
			case "bootstrap":
				fake.loaded = true
			case "kickstart":
				fake.loaded, fake.running = true, true
			case "bootout":
				fake.loaded, fake.running = false, false
			case "enable":
				fake.enabled = true
			}
		} else {
			args = args[1:]
			switch args[0] {
			case "show":
				if len(args) == 2 {
					return []byte("Version=255"), nil
				}
				loadState, active, sub, enabled := "not-found", "inactive", "dead", "disabled"
				if fake.loaded {
					loadState = "loaded"
				}
				if fake.running {
					active, sub = "active", "running"
				}
				if fake.enabled {
					enabled = "enabled"
				}
				return []byte("LoadState=" + loadState + "\nActiveState=" + active + "\nSubState=" + sub + "\nUnitFileState=" + enabled + "\nResult=success\n"), nil
			case "daemon-reload":
				_, err := os.Stat(fake.manager.unitPath())
				fake.loaded = err == nil
			case "start":
				fake.running = true
			case "stop":
				fake.running = false
			case "enable":
				fake.enabled = true
			case "disable":
				fake.enabled = false
			}
		}
		return nil, nil
	}
	return fake
}

func testSource(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "source-relay")
	if err := os.WriteFile(path, []byte("first binary"), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLifecycleAndUpgrade(t *testing.T) {
	for _, platform := range []string{"darwin", "linux"} {
		t.Run(platform, func(t *testing.T) {
			fake := newFake(t, platform)
			manager := fake.manager
			home := filepath.Join(manager.UserHome, "Relay data")
			source := testSource(t, manager.UserHome)
			var output bytes.Buffer
			run := func(action, relayHome string) {
				t.Helper()
				if err := manager.Execute(context.Background(), action, relayHome, source, &output); err != nil {
					t.Fatalf("%s: %v", action, err)
				}
			}
			run("status", "")
			if !strings.Contains(output.String(), "not installed") {
				t.Fatal(output.String())
			}
			run("install", home)
			if !fake.running || !fake.enabled {
				t.Fatal("not enabled and running")
			}
			sentinel := filepath.Join(home, "identity-sentinel")
			os.WriteFile(sentinel, []byte("preserve identity and data"), 0600)
			installed, err := manager.load()
			if err != nil {
				t.Fatal(err)
			}
			copied, _ := os.ReadFile(installed.Binary)
			if string(copied) != "first binary" {
				t.Fatal("binary not installed")
			}
			callCount := len(fake.calls)
			run("install", home)
			for _, call := range fake.calls[callCount:] {
				if strings.Contains(call, "bootout") || strings.Contains(call, " stop ") {
					t.Fatal("idempotent install stopped daemon")
				}
			}
			if err := manager.Execute(context.Background(), "install", home+"-other", source, &output); err == nil {
				t.Fatal("changed service home")
			}
			os.WriteFile(source, []byte("second binary"), 0700)
			run("install", home)
			previous, _ := os.ReadFile(installed.Binary + ".previous")
			if string(previous) != "first binary" {
				t.Fatal("previous binary missing")
			}
			copied, _ = os.ReadFile(installed.Binary)
			if string(copied) != "second binary" {
				t.Fatal("upgrade missing")
			}
			run("status", "")
			if !strings.Contains(output.String(), home) {
				t.Fatal("home absent from status")
			}
			run("stop", "")
			if fake.running {
				t.Fatal("stop failed")
			}
			run("stop", "")
			run("start", "")
			run("restart", "")
			run("uninstall", "")
			run("uninstall", "")
			if fake.running {
				t.Fatal("uninstall left daemon running")
			}
			if _, err := os.Stat(installed.UnitPath); !os.IsNotExist(err) {
				t.Fatal("unit not removed")
			}
			data, _ := os.ReadFile(sentinel)
			if string(data) != "preserve identity and data" {
				t.Fatal("data removed")
			}
		})
	}
}

func TestRefusesUnownedOrEditedServices(t *testing.T) {
	fake := newFake(t, "darwin")
	manager := fake.manager
	source := testSource(t, manager.UserHome)
	os.MkdirAll(filepath.Dir(manager.unitPath()), 0700)
	os.WriteFile(manager.unitPath(), []byte("unrelated service"), 0600)
	if err := manager.Execute(context.Background(), "install", filepath.Join(manager.UserHome, "relay"), source, &bytes.Buffer{}); err == nil {
		t.Fatal("accepted unowned file")
	}
	data, _ := os.ReadFile(manager.unitPath())
	if string(data) != "unrelated service" {
		t.Fatal("overwrote unowned file")
	}
	os.Remove(manager.unitPath())
	if err := manager.Execute(context.Background(), "install", filepath.Join(manager.UserHome, "relay"), source, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(manager.unitPath(), []byte("user changed unit"), 0600)
	if err := manager.Execute(context.Background(), "uninstall", "", source, &bytes.Buffer{}); err == nil {
		t.Fatal("removed edited unit")
	}
}

func TestServiceFailures(t *testing.T) {
	fake := newFake(t, "linux")
	manager := fake.manager
	source := testSource(t, manager.UserHome)
	home := filepath.Join(manager.UserHome, "relay")
	fake.fail = "--property=Version"
	if err := manager.Execute(context.Background(), "install", home, source, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "user manager unavailable") {
		t.Fatal(err)
	}
	if _, err := os.Stat(manager.unitPath()); !os.IsNotExist(err) {
		t.Fatal("wrote unit without manager")
	}
	fake.fail = ""
	fake.running = true
	if err := manager.Execute(context.Background(), "install", home, source, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "daemon already runs") {
		t.Fatal(err)
	}
	fake.running = false
	fake.fail = " start "
	if err := manager.Execute(context.Background(), "install", home, source, &bytes.Buffer{}); err == nil {
		t.Fatal("reported failed start as success")
	}
	fake.fail = ""
	if err := manager.Execute(context.Background(), "install", home, source, &bytes.Buffer{}); err != nil {
		t.Fatal("failed install was not recoverable", err)
	}
}

func TestRenderAndPaths(t *testing.T) {
	for _, platform := range []string{"darwin", "linux"} {
		fake := newFake(t, platform)
		manager := fake.manager
		installed := Installation{Home: "/data/Relay & <test> \"quoted\" $cash%node", Binary: "/opt/Relay/bin/agent-relay"}
		unit, err := manager.render(installed)
		if err != nil {
			t.Fatal(err)
		}
		if platform == "darwin" {
			if !strings.Contains(unit, "&amp;") || !strings.Contains(unit, "&lt;test&gt;") || !strings.Contains(unit, "<integer>45</integer>") {
				t.Fatal(unit)
			}
		} else {
			if !strings.Contains(unit, "$$cash%%node") || !strings.Contains(unit, "WorkingDirectory=/") || !strings.Contains(unit, "TimeoutStopSec=45") {
				t.Fatal(unit)
			}
		}
		installed.Home = "/home/line\nbreak"
		if _, err := manager.render(installed); err == nil {
			t.Fatal("accepted newline")
		}
		manager.Name = "../bad"
		if err := manager.validate(); err == nil {
			t.Fatal("accepted invalid service name")
		}
	}
	fake := newFake(t, "linux")
	fake.manager.ConfigHome = "/custom/config"
	fake.manager.DataHome = "/custom/data"
	if fake.manager.unitPath() != "/custom/config/systemd/user/relay-test.service" || fake.manager.installDir() != "/custom/data/agent-relay/services/relay-test" {
		t.Fatal("XDG paths ignored")
	}
}

func TestStatusFailureNotReportedAsStopped(t *testing.T) {
	for _, platform := range []string{"darwin", "linux"} {
		fake := newFake(t, platform)
		fake.fail = " "
		if _, err := fake.manager.state(context.Background()); err == nil {
			t.Fatal("manager failure reported as stopped")
		}
	}
}

func TestTemplatesMatchFiles(t *testing.T) {
	for path, expected := range map[string]string{"launchd.plist.tmpl": launchTemplate, "systemd.service.tmpl": systemdTemplate} {
		data, err := os.ReadFile(filepath.Join("..", "..", "templates", path))
		if err != nil || string(data) != expected {
			t.Fatalf("template %s differs from renderer: %v", path, err)
		}
	}
}

func TestStartWaitsForHTTPReadiness(t *testing.T) {
	fake := newFake(t, "linux")
	source := testSource(t, fake.manager.UserHome)
	probes := 0
	fake.manager.Ready = func(context.Context, string) error {
		probes++
		if probes == 1 {
			return errors.New("listener not ready")
		}
		return nil
	}
	if err := fake.manager.Execute(context.Background(), "install", filepath.Join(fake.manager.UserHome, "relay"), source, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if probes != 2 {
		t.Fatalf("startup did not wait for HTTP readiness: %d probes", probes)
	}
	fake.manager.Ready = func(context.Context, string) error { return errors.New("listener failed") }
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := fake.manager.Execute(ctx, "start", "", source, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "startup was not confirmed") {
		t.Fatal(err)
	}
}

func TestDarwinStartsPendingBootstrap(t *testing.T) {
	fake := newFake(t, "darwin")
	installed := &Installation{Home: fake.manager.UserHome, UnitPath: fake.manager.unitPath()}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := fake.manager.start(ctx, installed); err != nil {
		t.Fatal(err)
	}
	calls := strings.Join(fake.calls, "\n")
	bootstrap := strings.Index(calls, "launchctl bootstrap ")
	kickstart := strings.Index(calls, "launchctl kickstart ")
	if bootstrap < 0 || kickstart <= bootstrap || !fake.running {
		t.Fatalf("pending service was not explicitly started: %s", calls)
	}
	fake.calls = nil
	if err := fake.manager.start(ctx, installed); err != nil {
		t.Fatal(err)
	}
	for _, call := range fake.calls {
		if strings.Contains(call, "kickstart") || strings.Contains(call, "bootstrap") {
			t.Fatalf("already running service started again: %s", call)
		}
	}
}

func TestReadinessStateTimeoutIncludesDiagnostics(t *testing.T) {
	fake := newFake(t, "darwin")
	run := fake.manager.Run
	fake.manager.Run = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		if args[0] == "print" && fake.running {
			return nil, context.DeadlineExceeded
		}
		return run(ctx, command, args...)
	}
	err := fake.manager.start(context.Background(), &Installation{Home: fake.manager.UserHome, UnitPath: fake.manager.unitPath()})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lost timeout cause: %v", err)
	}
	for _, want := range []string{"startup was not confirmed", "service status --name relay-test", filepath.Join(fake.manager.UserHome, "logs"), "launchctl print"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("missing %q: %v", want, err)
		}
	}
}

func TestMissingHomeCanBeUninstalled(t *testing.T) {
	fake := newFake(t, "linux")
	source := testSource(t, fake.manager.UserHome)
	home := filepath.Join(fake.manager.UserHome, "relay")
	if err := fake.manager.Execute(context.Background(), "install", home, source, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	fake.running = false
	fake.manager.Running = func(string) (bool, error) { return false, os.ErrNotExist }
	if err := os.RemoveAll(home); err != nil {
		t.Fatal(err)
	}
	if err := fake.manager.Execute(context.Background(), "uninstall", "", source, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
}

func TestRefusesUnownedAndSymlinkBinaries(t *testing.T) {
	for _, platform := range []string{"darwin", "linux"} {
		for _, kind := range []string{"unowned", "symlink", "managed-symlink"} {
			t.Run(platform+"/"+kind, func(t *testing.T) {
				fake := newFake(t, platform)
				manager := fake.manager
				source := testSource(t, manager.UserHome)
				home := filepath.Join(manager.UserHome, "relay")
				if err := os.MkdirAll(manager.installDir(), 0700); err != nil {
					t.Fatal(err)
				}
				binary := filepath.Join(manager.installDir(), "agent-relay")
				if kind == "managed-symlink" {
					if err := manager.Execute(context.Background(), "install", home, source, &bytes.Buffer{}); err != nil {
						t.Fatal(err)
					}
					if err := os.Remove(binary); err != nil {
						t.Fatal(err)
					}
				}
				if kind == "unowned" {
					if err := os.WriteFile(binary, []byte("unrelated executable"), 0700); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Symlink(source, binary); err != nil {
					t.Fatal(err)
				}
				callCount := len(fake.calls)
				err := manager.Execute(context.Background(), "install", home, source, &bytes.Buffer{})
				if err == nil || !strings.Contains(err.Error(), "refusing unowned or nonregular") {
					t.Fatal("unsafe install accepted", err)
				}
				for _, call := range fake.calls[callCount:] {
					if strings.Contains(call, "bootout") || strings.Contains(call, " stop ") {
						t.Fatal("unsafe install stopped the service")
					}
				}
				data, err := os.ReadFile(binary)
				if err != nil || kind == "unowned" && string(data) != "unrelated executable" || kind != "unowned" && string(data) != "first binary" {
					t.Fatal("existing executable changed", err)
				}
			})
		}
	}
}
