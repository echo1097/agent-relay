package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"agent-relay/internal/config"
)

func runApp(args []string, output, errorOutput io.Writer) error {
	if len(args) == 1 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h") {
		_, err := fmt.Fprintln(output, "Usage: agent-relay app [--home PATH]\n\nOpen the desktop app. Run npm install and npm run build in desktop first.\nRun from the source checkout, or set AGENT_RELAY_APP_DIR to its desktop directory.")
		return err
	}
	flags := flag.NewFlagSet("app", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	home := flags.String("home", "", "application directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("app does not accept arguments; run agent-relay app help")
	}
	paths, err := config.Resolve(*home)
	if err != nil {
		return err
	}
	executablePath, err := os.Executable()
	if err != nil {
		return err
	}
	appDir, err := findAppDir()
	if err != nil {
		return err
	}
	electronName := "electron"
	if os.PathSeparator == '\\' {
		electronName += ".cmd"
	}
	electronPath := filepath.Join(appDir, "node_modules", ".bin", electronName)
	if _, err := os.Stat(electronPath); err != nil {
		return fmt.Errorf("desktop dependencies missing: run npm install in %s", appDir)
	}
	if _, err := os.Stat(filepath.Join(appDir, "dist", "index.html")); err != nil {
		return fmt.Errorf("desktop build missing: run npm run build in %s", appDir)
	}
	appArgs := []string{appDir, "--relay-binary", executablePath, "--relay-home", paths.Home}
	appCommand := exec.Command(electronPath, appArgs...)
	if runtime.GOOS == "darwin" {
		runtimePath := filepath.Join(appDir, "node_modules", "electron", "dist", "Electron.app")
		appCommand = exec.Command("open", append([]string{"-n", "-a", runtimePath, "--args"}, appArgs...)...)
	}
	appCommand.Dir = appDir
	appCommand.Stdout = output
	appCommand.Stderr = errorOutput
	if runtime.GOOS == "darwin" {
		if err := appCommand.Run(); err != nil {
			return fmt.Errorf("open desktop app: %w", err)
		}
		return nil
	}
	if err := appCommand.Start(); err != nil {
		return fmt.Errorf("open desktop app: %w", err)
	}
	return appCommand.Process.Release()
}

func findAppDir() (string, error) {
	appDir := os.Getenv("AGENT_RELAY_APP_DIR")
	if appDir != "" {
		return filepath.Abs(appDir)
	}
	workDir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	executablePath, err := os.Executable()
	if err != nil {
		return "", err
	}
	candidates := []string{filepath.Join(workDir, "desktop"), workDir, filepath.Join(filepath.Dir(executablePath), "..", "desktop")}
	for _, candidate := range candidates {
		if _, err := os.Stat(filepath.Join(candidate, "electron", "main.cjs")); err == nil {
			return filepath.Abs(candidate)
		}
	}
	return "", errors.New("desktop app not found: run from the source checkout or set AGENT_RELAY_APP_DIR to its desktop directory")
}
