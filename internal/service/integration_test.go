package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent-relay/internal/protocol"
	"agent-relay/internal/setup"

	"github.com/BurntSushi/toml"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestOSServiceLifecycle(t *testing.T) {
	if os.Getenv("AGENT_RELAY_SERVICE_TEST") != "1" {
		t.Skip("set AGENT_RELAY_SERVICE_TEST=1 and AGENT_RELAY_TEST_BINARY to exercise the real user service manager")
	}
	source := os.Getenv("AGENT_RELAY_TEST_BINARY")
	if !filepath.IsAbs(source) {
		t.Fatal("AGENT_RELAY_TEST_BINARY must be an absolute built Relay binary path")
	}
	manager, err := New(fmt.Sprintf("relay-test-%d", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	home := filepath.Join(root, "Relay & $cash%node")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	configData := fmt.Sprintf("[network]\ndevelopment = true\nbind_address = '127.0.0.1'\nport = %d\n", port)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(configData), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var output bytes.Buffer
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		if err := manager.Execute(cleanupCtx, "uninstall", "", source, &output); err != nil {
			t.Errorf("service cleanup failed for %s: %v", manager.Name, err)
		} else {
			if err := os.RemoveAll(manager.installDir()); err != nil {
				t.Error(err)
			}
			matches, _ := filepath.Glob(manager.unitPath() + ".agent-relay-*")
			for _, path := range matches {
				if err := os.Remove(path); err != nil {
					t.Error(err)
				}
			}
		}
		t.Log(output.String())
	})
	run := func(action, relayHome string) {
		t.Helper()
		if err := manager.Execute(ctx, action, relayHome, source, &output); err != nil {
			for _, name := range []string{"agent-relay.log", "service.log"} {
				data, _ := os.ReadFile(filepath.Join(home, "logs", name))
				t.Log(string(data))
			}
			t.Fatalf("%s: %v", action, err)
		}
	}
	run("install", home)
	installed, err := manager.load()
	if err != nil {
		t.Fatal(err)
	}
	hello := func() protocol.Hello {
		t.Helper()
		client := http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}}
		defer client.CloseIdleConnections()
		response, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/v1/hello", port))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var result protocol.Hello
		if response.StatusCode != 200 || json.NewDecoder(response.Body).Decode(&result) != nil || result.Node.ID == "" {
			t.Fatalf("invalid hello: %+v", result)
		}
		return result
	}
	original := hello()
	run("install", home)
	run("status", "")
	for _, clientName := range []string{"codex", "claude"} {
		configPath := filepath.Join(root, clientName+"-config")
		result, err := setup.Configure(setup.Client{Name: clientName, Path: configPath}, installed.Binary, home, false)
		if err != nil || !result.Changed {
			t.Fatalf("setup %s: %v", clientName, err)
		}
		data, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatal(err)
		}
		var clientConfig struct {
			Servers map[string]struct {
				Command string   `json:"command" toml:"command"`
				Args    []string `json:"args" toml:"args"`
			} `json:"mcpServers" toml:"mcp_servers"`
		}
		if clientName == "codex" {
			_, err = toml.Decode(string(data), &clientConfig)
		} else {
			err = json.Unmarshal(data, &clientConfig)
		}
		if err != nil {
			t.Fatal(err)
		}
		server := clientConfig.Servers["agent-relay"]
		connection, err := sdk.NewClient(&sdk.Implementation{Name: "service-test", Version: "1"}, nil).Connect(ctx, &sdk.CommandTransport{Command: exec.Command(server.Command, server.Args...)}, nil)
		if err != nil {
			t.Fatal(err)
		}
		tools, err := connection.ListTools(ctx, &sdk.ListToolsParams{})
		if err != nil || len(tools.Tools) != 8 {
			connection.Close()
			t.Fatalf("tools: %+v %v", tools, err)
		}
		toolResult, err := connection.CallTool(ctx, &sdk.CallToolParams{Name: "relay.update_status", Arguments: map[string]any{}})
		if err != nil || toolResult.IsError {
			connection.Close()
			t.Fatalf("MCP registration: %+v %v", toolResult, err)
		}
		if err := connection.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if upgrade := os.Getenv("AGENT_RELAY_UPGRADE_BINARY"); upgrade != "" {
		source = upgrade
		run("install", "")
		upgraded := hello()
		if upgraded.Node.ID != original.Node.ID || upgraded.Version == original.Version {
			t.Fatalf("upgrade did not preserve identity and change version: %+v", upgraded)
		}
		if _, err := os.Stat(installed.Binary + ".previous"); err != nil {
			t.Fatal("missing previous binary", err)
		}
	}
	logPath := filepath.Join(home, "logs", "agent-relay.log")
	beforeCrash, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if manager.Platform == "darwin" {
		_, err = manager.command(ctx, "kill", "SIGKILL", manager.target())
	} else {
		_, err = manager.command(ctx, "kill", "--signal=SIGKILL", manager.Name+".service")
	}
	if err != nil {
		t.Fatal(err)
	}
	restarted := false
	for deadline := time.Now().Add(25 * time.Second); time.Now().Before(deadline); {
		logs, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(string(logs), "daemon started") > strings.Count(string(beforeCrash), "daemon started") {
			restarted = true
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
	if !restarted || hello().Node.ID != original.Node.ID {
		t.Fatal("supervisor did not recover the crashed daemon with its identity intact")
	}
	run("restart", "")
	if hello().Node.ID != original.Node.ID {
		t.Fatal("restart changed identity")
	}
	run("stop", "")
	if running, err := manager.Running(filepath.Join(home, "daemon.lock")); err != nil || running {
		t.Fatal("daemon still running", err)
	}
	run("stop", "")
	run("start", "")
	if hello().Node.ID != original.Node.ID {
		t.Fatal("start changed identity")
	}
	run("uninstall", "")
	if _, err := os.Stat(filepath.Join(home, "relay.db")); err != nil {
		t.Fatal("uninstall removed database", err)
	}
	logs, _ := os.ReadFile(filepath.Join(home, "logs", "agent-relay.log"))
	if strings.Count(string(logs), "daemon stopped") < 3 {
		t.Fatal("missing graceful shutdowns", string(logs))
	}
	run("status", "")
	t.Logf("verified %s service, stable node %s, all 8 MCP tools, and graceful shutdown", manager.Platform, original.Node.ID)
}
