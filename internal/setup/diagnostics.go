package setup

import (
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type mcpEntry struct {
	Command  string   `toml:"command" json:"command"`
	Args     []string `toml:"args" json:"args"`
	Disabled bool     `toml:"disabled" json:"disabled"`
	Enabled  *bool    `toml:"enabled" json:"enabled"`
	Type     string   `toml:"type" json:"type"`
}

func Check(client Client, relayHome string) error {
	data, err := os.ReadFile(client.Path)
	if err != nil {
		return errors.New("MCP configuration is missing or unreadable")
	}
	var entry mcpEntry
	if client.Name == "codex" {
		var config struct {
			Servers map[string]mcpEntry `toml:"mcp_servers"`
		}
		if _, err := toml.Decode(string(data), &config); err != nil {
			return errors.New("MCP configuration contains invalid TOML")
		}
		entry = config.Servers["agent-relay"]
	} else {
		var config struct {
			Servers map[string]mcpEntry `json:"mcpServers"`
		}
		if err := json.Unmarshal(data, &config); err != nil {
			return errors.New("MCP configuration contains invalid JSON")
		}
		entry = config.Servers["agent-relay"]
	}
	if entry.Disabled || entry.Enabled != nil && !*entry.Enabled || entry.Type != "" && entry.Type != "stdio" {
		return errors.New("Agent Relay MCP entry must be enabled and use stdio")
	}
	info, err := os.Stat(entry.Command)
	if !filepath.IsAbs(entry.Command) || err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return errors.New("Agent Relay MCP command must reference an existing executable by absolute path")
	}
	if len(entry.Args) == 0 || entry.Args[0] != "mcp" {
		return errors.New("Agent Relay MCP arguments must include mcp --home PATH")
	}
	flags := flag.NewFlagSet("mcp", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	configuredHome := flags.String("home", "", "")
	for _, name := range []string{"agent-id", "name", "provider", "task", "project", "repository", "branch"} {
		flags.String(name, "", "")
	}
	if err := flags.Parse(entry.Args[1:]); err != nil || flags.NArg() != 0 {
		return errors.New("Agent Relay MCP arguments are invalid")
	}
	if *configuredHome != relayHome {
		return errors.New("Agent Relay MCP home does not match the directory being diagnosed")
	}
	return nil
}
