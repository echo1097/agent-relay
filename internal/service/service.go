package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"text/template"
	"time"

	"agent-relay/internal/config"
	"agent-relay/internal/daemon"
	"agent-relay/internal/discovery"
	"agent-relay/internal/fileedit"
	"agent-relay/internal/storage"
	"agent-relay/internal/tailscale"
)

type Manager struct {
	Platform   string
	UserHome   string
	ConfigHome string
	DataHome   string
	UserID     int
	Name       string
	Run        func(context.Context, string, ...string) ([]byte, error)
	Running    func(string) (bool, error)
	Ready      func(context.Context, string) error
}

type Installation struct {
	Home     string `json:"home"`
	Binary   string `json:"binary"`
	Source   string `json:"source"`
	UnitPath string `json:"unit_path"`
	Unit     string `json:"unit"`
}

type State struct {
	Loaded  bool
	Running bool
	Detail  string
}

func New(name string) (*Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	manager := &Manager{Platform: runtime.GOOS, UserHome: home, ConfigHome: os.Getenv("XDG_CONFIG_HOME"), DataHome: os.Getenv("XDG_DATA_HOME"), UserID: os.Getuid(), Name: name, Running: daemon.Running}
	manager.Ready = daemonReady
	manager.Run = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		commandCtx, cancel := context.WithTimeout(ctx, 55*time.Second)
		defer cancel()
		process := exec.CommandContext(commandCtx, command, args...)
		process.WaitDelay = time.Second
		data, err := process.CombinedOutput()
		if commandCtx.Err() != nil {
			return data, commandCtx.Err()
		}
		return data, err
	}
	return manager, manager.validate()
}

func (manager *Manager) validate() error {
	if manager.Platform != "darwin" && manager.Platform != "linux" {
		return errors.New("background services support macOS launchd and Linux systemd user services; Windows is future work")
	}
	if manager.UserID == 0 {
		return errors.New("run service commands as your normal login user, without sudo")
	}
	if !regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`).MatchString(manager.Name) {
		return errors.New("service name must start with a lowercase letter and contain at most 48 lowercase letters, digits, or hyphens")
	}
	for _, path := range []string{manager.UserHome, manager.ConfigHome, manager.DataHome} {
		if path != "" && (!filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\n\r")) {
			return errors.New("service directories must be absolute paths without control characters")
		}
	}
	return nil
}

func (manager *Manager) label() string { return "com.agent-relay." + manager.Name }

func (manager *Manager) unitPath() string {
	if manager.Platform == "darwin" {
		return filepath.Join(manager.UserHome, "Library", "LaunchAgents", manager.label()+".plist")
	}
	root := manager.ConfigHome
	if root == "" {
		root = filepath.Join(manager.UserHome, ".config")
	}
	return filepath.Join(root, "systemd", "user", manager.Name+".service")
}

func (manager *Manager) installDir() string {
	root := manager.DataHome
	if manager.Platform == "darwin" {
		root = filepath.Join(manager.UserHome, "Library", "Application Support")
	} else if root == "" {
		root = filepath.Join(manager.UserHome, ".local", "share")
	}
	return filepath.Join(root, "agent-relay", "services", manager.Name)
}

func (manager *Manager) target() string {
	return "gui/" + strconv.Itoa(manager.UserID) + "/" + manager.label()
}

func (manager *Manager) load() (*Installation, error) {
	data, _, err := fileedit.Read(filepath.Join(manager.installDir(), "install.json"))
	if err != nil {
		return nil, err
	}
	unit, unitInfo, err := fileedit.Read(manager.unitPath())
	if err != nil {
		return nil, err
	}
	if data == nil {
		if unitInfo != nil {
			return nil, fmt.Errorf("refusing to manage an existing unowned service file: %s", manager.unitPath())
		}
		return nil, nil
	}
	var installed Installation
	if json.Unmarshal(data, &installed) != nil || installed.Home == "" || installed.Binary != filepath.Join(manager.installDir(), "agent-relay") || installed.UnitPath != manager.unitPath() {
		return nil, errors.New("invalid service installation record; inspect install.json and restore its backup")
	}
	if !filepath.IsAbs(installed.Home) || strings.ContainsAny(installed.Home, "\x00\n\r") || installed.Unit == "" {
		return nil, errors.New("invalid home or definition in service installation record")
	}
	if unitInfo != nil && string(unit) != installed.Unit {
		return nil, fmt.Errorf("service file %s differs from its installation record; restore the file or its matching backup before retrying", manager.unitPath())
	}
	return &installed, nil
}

func systemdQuote(value string) string {
	value = strings.ReplaceAll(value, "%", "%%")
	value = strings.ReplaceAll(value, "$", "$$")
	return strconv.Quote(value)
}

func (manager *Manager) render(installed Installation) (string, error) {
	for _, value := range []string{installed.Home, installed.Binary} {
		if !filepath.IsAbs(value) || strings.ContainsAny(value, "\x00\n\r") {
			return "", errors.New("service paths must be absolute without control characters")
		}
	}
	searchPath := "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
	values := map[string]string{"Label": manager.label(), "Home": installed.Home, "Binary": installed.Binary, "Log": filepath.Join(installed.Home, "logs", "service.log"), "SearchPath": searchPath}
	source := launchTemplate
	if manager.Platform == "darwin" {
		for key, value := range values {
			var escaped bytes.Buffer
			if err := xml.EscapeText(&escaped, []byte(value)); err != nil {
				return "", err
			}
			values[key] = escaped.String()
		}
	} else {
		source = systemdTemplate
		values["Binary"] = systemdQuote(installed.Binary)
		values["Home"] = systemdQuote(installed.Home)
		values["SearchPath"] = strconv.Quote("PATH=" + searchPath)
	}
	parsed, err := template.New("service").Parse(source)
	if err != nil {
		return "", err
	}
	var result bytes.Buffer
	err = parsed.Execute(&result, values)
	return result.String(), err
}

func (manager *Manager) command(ctx context.Context, args ...string) ([]byte, error) {
	command := "launchctl"
	if manager.Platform == "linux" {
		command = "systemctl"
		args = append([]string{"--user"}, args...)
	}
	data, err := manager.Run(ctx, command, args...)
	if err != nil {
		return data, fmt.Errorf("%s %s failed: %w\n%s", command, strings.Join(args, " "), err, strings.TrimSpace(string(data)))
	}
	return data, nil
}

func (manager *Manager) state(ctx context.Context) (State, error) {
	if manager.Platform == "darwin" {
		data, err := manager.command(ctx, "print", manager.target())
		if err != nil {
			if strings.Contains(string(data), "Could not find service") {
				return State{Detail: "stopped (not loaded)"}, nil
			}
			return State{}, err
		}
		running := strings.Contains(string(data), "state = running")
		detail := "loaded; waiting or exited (inspect launchctl print and service.log)"
		if running {
			detail = "running"
		}
		return State{Loaded: true, Running: running, Detail: detail}, nil
	}
	data, err := manager.command(ctx, "show", manager.Name+".service", "--property=LoadState", "--property=ActiveState", "--property=SubState", "--property=UnitFileState", "--property=Result", "--no-pager")
	if err != nil && !strings.Contains(string(data), "LoadState=not-found") {
		return State{}, err
	}
	properties := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			properties[key] = value
		}
	}
	if properties["LoadState"] == "" {
		return State{}, errors.New("systemctl returned no service state; check the systemd user session")
	}
	return State{Loaded: properties["LoadState"] == "loaded", Running: properties["ActiveState"] == "active" && properties["SubState"] == "running", Detail: fmt.Sprintf("%s/%s; enabled=%s; result=%s", properties["ActiveState"], properties["SubState"], properties["UnitFileState"], properties["Result"])}, nil
}

func (manager *Manager) available(ctx context.Context) error {
	if manager.Platform == "darwin" {
		if _, err := manager.command(ctx, "print", "gui/"+strconv.Itoa(manager.UserID)); err != nil {
			return fmt.Errorf("a logged-in macOS GUI session is required: %w", err)
		}
		return nil
	}
	_, err := manager.command(ctx, "show", "--property=Version")
	if err != nil {
		return fmt.Errorf("systemd user manager unavailable; log in with a normal user session and check systemctl --user status: %w", err)
	}
	return nil
}

func (manager *Manager) waitStopped(ctx context.Context, installed *Installation) error {
	deadline := time.NewTimer(45 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		running, err := manager.running(filepath.Join(installed.Home, "daemon.lock"))
		if err != nil {
			return err
		}
		if !running {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("daemon did not release its lock within 45 seconds; inspect logs before retrying")
		case <-ticker.C:
		}
	}
}

func (manager *Manager) stop(ctx context.Context, installed *Installation) error {
	state, err := manager.state(ctx)
	if err != nil {
		return err
	}
	if !state.Running {
		running, err := manager.running(filepath.Join(installed.Home, "daemon.lock"))
		if err != nil {
			return err
		}
		if running {
			return errors.New("another daemon owns this home; stop it explicitly before managing the service")
		}
	}
	if manager.Platform == "darwin" {
		if state.Loaded {
			if _, err := manager.command(ctx, "bootout", manager.target()); err != nil {
				return err
			}
		}
	} else if state.Loaded {
		if _, err := manager.command(ctx, "stop", manager.Name+".service"); err != nil {
			return err
		}
	}
	return manager.waitStopped(ctx, installed)
}

func (manager *Manager) start(ctx context.Context, installed *Installation) error {
	state, err := manager.state(ctx)
	if err != nil {
		return err
	}
	if !state.Running {
		running, err := manager.running(filepath.Join(installed.Home, "daemon.lock"))
		if err != nil {
			return err
		}
		if running {
			return errors.New("another daemon already owns this Relay home; stop that foreground daemon before starting the service")
		}
	}
	if manager.Platform == "darwin" {
		if _, err := manager.command(ctx, "enable", manager.target()); err != nil {
			return err
		}
		if !state.Loaded {
			if _, err := manager.command(ctx, "bootstrap", "gui/"+strconv.Itoa(manager.UserID), installed.UnitPath); err != nil {
				return err
			}
		} else if !state.Running {
			if _, err := manager.command(ctx, "kickstart", manager.target()); err != nil {
				return err
			}
		}
	} else {
		if _, err := manager.command(ctx, "start", manager.Name+".service"); err != nil {
			return err
		}
	}
	readyCtx, cancelReady := context.WithTimeout(ctx, 12*time.Second)
	defer cancelReady()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	var readyErr error
	for {
		state, err := manager.state(readyCtx)
		if err != nil {
			return err
		}
		if state.Running {
			readyErr = manager.Ready(readyCtx, installed.Home)
			if readyErr == nil {
				running, err := manager.running(filepath.Join(installed.Home, "daemon.lock"))
				if err != nil {
					return err
				}
				if running {
					return nil
				}
			}
		}
		select {
		case <-readyCtx.Done():
			return fmt.Errorf("service was submitted but daemon startup was not confirmed; check Tailscale, %s, and service status: %w", filepath.Join(installed.Home, "logs", "agent-relay.log"), errors.Join(readyCtx.Err(), readyErr))
		case <-ticker.C:
		}
	}
}

func (manager *Manager) Execute(ctx context.Context, action, home, source string, output io.Writer) error {
	if err := manager.validate(); err != nil {
		return err
	}
	switch action {
	case "install", "uninstall", "start", "stop", "restart", "status":
	default:
		return fmt.Errorf("unknown service action %q", action)
	}
	if action != "install" && home != "" {
		return errors.New("--home is only used during service install; other actions use the recorded home")
	}
	if err := os.MkdirAll(manager.installDir(), 0700); err != nil {
		return err
	}
	return manager.withLock(func() error {
		installed, err := manager.load()
		if err != nil {
			return err
		}
		if action == "status" {
			if installed == nil {
				_, err := fmt.Fprintf(output, "Service %s: not installed\n", manager.Name)
				return err
			}
			state, err := manager.state(ctx)
			if err != nil {
				return err
			}
			running, err := manager.running(filepath.Join(installed.Home, "daemon.lock"))
			if err != nil {
				return err
			}
			fmt.Fprintf(output, "Service %s: %s\nDaemon lock held: %t\nHome: %s\nBinary: %s\nDefinition: %s\nLogs: %s\n", manager.Name, state.Detail, running, installed.Home, installed.Binary, installed.UnitPath, filepath.Join(installed.Home, "logs", "agent-relay.log"))
			if manager.Platform == "darwin" {
				fmt.Fprintf(output, "Startup logs: %s\n", filepath.Join(installed.Home, "logs", "service.log"))
			} else {
				fmt.Fprintf(output, "Startup logs: journalctl --user -u %s.service\n", manager.Name)
			}
			return nil
		}
		if installed == nil && action != "install" {
			if action == "uninstall" {
				_, err := fmt.Fprintln(output, "Service is already uninstalled; Relay data is retained.")
				return err
			}
			return errors.New("service is not installed; run agent-relay service install")
		}
		if err := manager.available(ctx); err != nil {
			return err
		}
		switch action {
		case "install":
			return manager.install(ctx, installed, home, source, output)
		case "start":
			err = manager.start(ctx, installed)
		case "stop":
			err = manager.stop(ctx, installed)
		case "restart":
			err = manager.stop(ctx, installed)
			if err == nil {
				err = manager.start(ctx, installed)
			}
		case "uninstall":
			err = manager.stop(ctx, installed)
			if err == nil && manager.Platform == "linux" {
				_, err = manager.command(ctx, "disable", manager.Name+".service")
			}
			if err == nil {
				err = os.Remove(installed.UnitPath)
				if errors.Is(err, os.ErrNotExist) {
					err = nil
				}
			}
			if err == nil && manager.Platform == "linux" {
				_, err = manager.command(ctx, "daemon-reload")
			}
			if err == nil {
				err = os.Remove(filepath.Join(manager.installDir(), "install.json"))
			}
			if err == nil {
				err = os.Remove(installed.Binary)
				if errors.Is(err, os.ErrNotExist) {
					err = nil
				}
			}
		}
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(output, "Service %s: %s completed. Relay data retained at %s.\n", manager.Name, action, installed.Home)
		return err
	})
}

func (manager *Manager) install(ctx context.Context, old *Installation, home, source string, output io.Writer) error {
	if home == "" && old != nil {
		home = old.Home
	}
	paths, err := config.Resolve(home)
	if err != nil {
		return err
	}
	if old != nil && old.Home != paths.Home {
		return fmt.Errorf("service already uses %s; uninstall it before choosing another home, or use another --name", old.Home)
	}
	if _, err := config.Load(paths.Config); err != nil {
		return err
	}
	if err := paths.Ensure(); err != nil {
		return err
	}
	installed := Installation{Home: paths.Home, Source: source, Binary: filepath.Join(manager.installDir(), "agent-relay"), UnitPath: manager.unitPath()}
	installed.Unit, err = manager.render(installed)
	if err != nil {
		return err
	}
	binaryInfo, binaryErr := os.Lstat(installed.Binary)
	if binaryErr != nil && !errors.Is(binaryErr, os.ErrNotExist) {
		return binaryErr
	}
	if binaryInfo != nil && (old == nil || !binaryInfo.Mode().IsRegular()) {
		return fmt.Errorf("refusing unowned or nonregular service binary at %s; review and move it aside before retrying", installed.Binary)
	}
	sameBinary, err := binaryMatches(source, installed.Binary)
	if err != nil {
		return err
	}
	_, unitErr := os.Stat(installed.UnitPath)
	if old != nil && old.Unit == installed.Unit && sameBinary && unitErr == nil {
		if manager.Platform == "linux" {
			if _, err := manager.command(ctx, "daemon-reload"); err != nil {
				return err
			}
			if _, err := manager.command(ctx, "enable", manager.Name+".service"); err != nil {
				return err
			}
		}
		if err := manager.start(ctx, old); err != nil {
			return err
		}
		_, err := fmt.Fprintf(output, "Service %s: already installed and running at %s\n", manager.Name, old.Home)
		return err
	}
	if old == nil {
		running, err := manager.running(paths.Lock)
		if err != nil {
			return err
		}
		if running {
			return errors.New("a daemon already runs for this home; stop the foreground daemon before service install (identity and data will be preserved)")
		}
		state, err := manager.state(ctx)
		if err != nil {
			return err
		}
		if state.Loaded {
			return errors.New("an unowned service with this name is already loaded; choose another --name")
		}
	} else if err := manager.stop(ctx, old); err != nil {
		return err
	}
	if !sameBinary {
		if _, err := os.Stat(installed.Binary); err == nil {
			if err := copyBinary(installed.Binary, installed.Binary+".previous"); err != nil {
				return err
			}
		}
		if err := copyBinary(source, installed.Binary); err != nil {
			return err
		}
	}
	unitResult, err := fileedit.Update(installed.UnitPath, func(data []byte) ([]byte, error) {
		if len(data) != 0 && (old == nil || string(data) != old.Unit) {
			return nil, errors.New("service definition changed during install; retry after reviewing it")
		}
		return []byte(installed.Unit), nil
	})
	if unitResult.Backup != "" {
		fmt.Fprintf(output, "Service definition backup: %s\n", unitResult.Backup)
	}
	if err != nil {
		return err
	}
	record, err := json.MarshalIndent(installed, "", "  ")
	if err != nil {
		return err
	}
	recordResult, err := fileedit.Update(filepath.Join(manager.installDir(), "install.json"), func(data []byte) ([]byte, error) { return append(record, '\n'), nil })
	if recordResult.Backup != "" {
		fmt.Fprintf(output, "Installation record backup: %s\n", recordResult.Backup)
	}
	if err != nil {
		return err
	}
	if manager.Platform == "linux" {
		if _, err := manager.command(ctx, "daemon-reload"); err != nil {
			return err
		}
		if _, err := manager.command(ctx, "enable", manager.Name+".service"); err != nil {
			return err
		}
	}
	if err := manager.start(ctx, &installed); err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "Service %s installed and running.\nHome: %s\nBinary: %s\nDefinition: %s\nLogs: %s\n", manager.Name, paths.Home, installed.Binary, installed.UnitPath, paths.Logs)
	return err
}

func binaryMatches(source, destination string) (bool, error) {
	hashFile := func(path string) ([]byte, error) {
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			return nil, fmt.Errorf("%s is not a regular executable; symlinks are not accepted", path)
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		openedInfo, err := file.Stat()
		if err != nil {
			return nil, err
		}
		if !os.SameFile(info, openedInfo) {
			return nil, fmt.Errorf("%s changed while inspecting the executable", path)
		}
		hash := sha256.New()
		if _, err := io.Copy(hash, file); err != nil {
			return nil, err
		}
		return hash.Sum(nil), nil
	}
	sourceHash, err := hashFile(source)
	if err != nil {
		return false, err
	}
	destHash, err := hashFile(destination)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return bytes.Equal(sourceHash, destHash), nil
}

func copyBinary(source, destination string) error {
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer file.Close()
	temp, err := os.CreateTemp(filepath.Dir(destination), ".agent-relay-binary-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	_, copyErr := io.Copy(temp, file)
	if copyErr == nil {
		copyErr = temp.Chmod(0700)
	}
	if copyErr == nil {
		copyErr = temp.Sync()
	}
	if err := errors.Join(copyErr, temp.Close()); err != nil {
		return err
	}
	return os.Rename(temp.Name(), destination)
}

func (manager *Manager) running(path string) (bool, error) {
	running, err := manager.Running(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return running, err
}

func daemonReady(ctx context.Context, home string) error {
	cfg, err := config.Load(filepath.Join(home, "config.toml"))
	if err != nil {
		return err
	}
	address := cfg.Network.BindAddress
	if !cfg.Network.Development {
		state, err := tailscale.New().Status(ctx)
		if err != nil {
			return err
		}
		if !state.Connected {
			return errors.New("Tailscale is not connected")
		}
		address = state.IP
	}
	store, err := storage.Inspect(ctx, filepath.Join(home, "relay.db"))
	if err != nil {
		return err
	}
	node, nodeErr := store.ExistingNode(ctx)
	if err := errors.Join(nodeErr, store.Close()); err != nil {
		return err
	}
	runtimeState, err := daemon.ReadRuntime(filepath.Join(home, "daemon.lock"))
	if err != nil {
		return err
	}
	if runtimeState.NodeID != node.ID || runtimeState.Address != net.JoinHostPort(address, strconv.Itoa(cfg.Network.Port)) {
		return errors.New("service listener does not match this Relay home's node identity and configured address")
	}
	hello, err := discovery.NewProber().Hello(ctx, address, cfg.Network.Port)
	if err != nil {
		return err
	}
	if hello.Node.ID != node.ID || hello.Version != runtimeState.Version {
		return errors.New("service listener identity or version differs from this Relay home's daemon; inspect the running process and port")
	}
	return nil
}
