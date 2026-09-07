package setup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"

	"agent-relay/internal/fileedit"
	"github.com/BurntSushi/toml"
)

type Client struct {
	Name     string
	Path     string
	Detected bool
}

type Environment struct {
	Home     string
	Getenv   func(string) string
	LookPath func(string) (string, error)
	Exists   func(string) bool
}

func LocalEnvironment() (Environment, error) {
	home, err := os.UserHomeDir()
	return Environment{Home: home, Getenv: os.Getenv, LookPath: exec.LookPath, Exists: func(path string) bool { _, err := os.Lstat(path); return !errors.Is(err, os.ErrNotExist) }}, err
}

func Detect(env Environment) ([]Client, error) {
	codexHome := env.Getenv("CODEX_HOME")
	if codexHome == "" {
		codexHome = filepath.Join(env.Home, ".codex")
	}
	claudePath := filepath.Join(env.Home, ".claude.json")
	if claudeHome := env.Getenv("CLAUDE_CONFIG_DIR"); claudeHome != "" {
		claudePath = filepath.Join(claudeHome, ".claude.json")
	}
	clients := []Client{{Name: "codex", Path: filepath.Join(codexHome, "config.toml")}, {Name: "claude", Path: claudePath}}
	for index := range clients {
		client := &clients[index]
		path, err := filepath.Abs(client.Path)
		if err != nil {
			return nil, err
		}
		client.Path = path
		_, commandErr := env.LookPath(client.Name)
		client.Detected = commandErr == nil || env.Exists(client.Path)
		if client.Name == "codex" {
			client.Detected = client.Detected || env.Exists("/Applications/Codex.app") || env.Exists(filepath.Join(env.Home, "Applications", "Codex.app"))
		}
	}
	return clients, nil
}

func Configure(client Client, binaryPath, relayHome string, replace bool) (fileedit.Result, error) {
	if !filepath.IsAbs(binaryPath) || !filepath.IsAbs(relayHome) {
		return fileedit.Result{}, errors.New("MCP binary and Relay home must be absolute paths")
	}
	return fileedit.Update(client.Path, func(data []byte) ([]byte, error) {
		return edit(data, client.Name, binaryPath, relayHome, replace)
	})
}

func edit(data []byte, clientName, binaryPath, relayHome string, replace bool) ([]byte, error) {
	root := map[string]any{}
	key := "mcp_servers"
	if clientName == "codex" {
		if _, err := toml.Decode(string(data), &root); err != nil {
			return nil, errors.New("malformed TOML; repair the file and retry (no config changes made)")
		}
	} else if clientName == "claude" {
		key = "mcpServers"
		if data != nil {
			decoder := json.NewDecoder(bytes.NewReader(data))
			decoder.UseNumber()
			value, err := readJSON(decoder)
			if err != nil {
				return nil, errors.New("malformed JSON or duplicate keys; repair the file and retry (no config changes made)")
			}
			var ok bool
			root, ok = value.(map[string]any)
			if !ok {
				return nil, errors.New("JSON configuration must be an object")
			}
			if _, err := decoder.Token(); err != io.EOF {
				return nil, errors.New("JSON configuration contains trailing content")
			}
		}
	} else {
		return nil, fmt.Errorf("unsupported client %q", clientName)
	}
	servers := map[string]any{}
	if value, exists := root[key]; exists {
		var ok bool
		servers, ok = value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s must be an object/table", key)
		}
	}
	entry := map[string]any{"command": binaryPath, "args": []any{"mcp", "--home", relayHome}}
	if clientName == "claude" {
		entry["type"] = "stdio"
	}
	if current, exists := servers["agent-relay"]; exists {
		if reflect.DeepEqual(current, entry) {
			return data, nil
		}
		if !replace {
			return nil, errors.New("agent-relay MCP entry already exists with different settings; review it, then use --replace to replace only that entry")
		}
	}
	servers["agent-relay"] = entry
	root[key] = servers
	if clientName == "claude" {
		encoded, err := json.MarshalIndent(root, "", "  ")
		return append(encoded, '\n'), err
	}
	var buffer bytes.Buffer
	if err := toml.NewEncoder(&buffer).Encode(root); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func readJSON(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}
	switch delimiter {
	case '{':
		result := map[string]any{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, errors.New("invalid key")
			}
			if _, exists := result[key]; exists {
				return nil, errors.New("duplicate key")
			}
			value, err := readJSON(decoder)
			if err != nil {
				return nil, err
			}
			result[key] = value
		}
		_, err := decoder.Token()
		return result, err
	case '[':
		result := []any{}
		for decoder.More() {
			value, err := readJSON(decoder)
			if err != nil {
				return nil, err
			}
			result = append(result, value)
		}
		_, err := decoder.Token()
		return result, err
	default:
		return nil, errors.New("unexpected delimiter")
	}
}
