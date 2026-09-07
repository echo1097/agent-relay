package setup

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"agent-relay/internal/fileedit"
)

const instructionStart = "<!-- agent-relay:startup:start -->"
const instructionEnd = "<!-- agent-relay:startup:end -->"

func InstructionPath(env Environment, clientName string) (string, error) {
	paths, err := InstructionPaths(env, clientName)
	if err != nil {
		return "", err
	}
	if clientName == "codex" {
		data, _, err := fileedit.Read(paths[1])
		if err != nil {
			return "", err
		}
		if len(strings.TrimSpace(string(data))) > 0 {
			return paths[1], nil
		}
	}
	return paths[0], nil
}

func InstructionPaths(env Environment, clientName string) ([]string, error) {
	switch clientName {
	case "codex":
		root := env.Getenv("CODEX_HOME")
		if root == "" {
			root = filepath.Join(env.Home, ".codex")
		}
		return []string{filepath.Join(root, "AGENTS.md"), filepath.Join(root, "AGENTS.override.md")}, nil
	case "claude":
		root := env.Getenv("CLAUDE_CONFIG_DIR")
		if root == "" {
			root = filepath.Join(env.Home, ".claude")
		}
		return []string{filepath.Join(root, "CLAUDE.md")}, nil
	default:
		return nil, fmt.Errorf("unsupported client %q", clientName)
	}
}

func instructionBlock(skillPath string) string {
	return instructionStart + "\n" +
		"## Agent Relay startup\n\n" +
		"When Agent Relay is available, read its skill at " + strconv.Quote(skillPath) + " before beginning work on the first turn. Follow it throughout the session.\n" +
		"Check the Relay inbox at the start of every turn and again before your final response. Publish and refresh your work status as described in the skill.\n" +
		"If Relay is unavailable, continue the user's task without retry loops. These instructions do not grant permission to contact peers or change client approval settings.\n" +
		instructionEnd
}

func SyncInstructions(path, skillPath string, remove bool) (fileedit.Result, error) {
	if remove {
		data, _, err := fileedit.Read(path)
		if err != nil || data == nil {
			return fileedit.Result{}, err
		}
	}
	return fileedit.Update(path, func(data []byte) ([]byte, error) {
		text := string(data)
		startCount := strings.Count(text, instructionStart)
		endCount := strings.Count(text, instructionEnd)
		if startCount == 0 && endCount == 0 {
			if remove {
				return data, nil
			}
			return []byte(text + "\n\n" + instructionBlock(skillPath) + "\n"), nil
		}
		start := strings.Index(text, instructionStart)
		end := strings.Index(text, instructionEnd)
		if startCount != 1 || endCount != 1 || end < start {
			return nil, errors.New("incomplete or duplicate Agent Relay startup markers; review the instruction file before retrying")
		}
		startEnd := start + len(instructionStart)
		endEnd := end + len(instructionEnd)
		if start > 0 && text[start-1] != '\n' || startEnd >= len(text) || text[startEnd] != '\n' || end == 0 || text[end-1] != '\n' || endEnd < len(text) && text[endEnd] != '\n' {
			return nil, errors.New("Agent Relay startup markers must be on separate lines; review the instruction file before retrying")
		}
		end += len(instructionEnd)
		if remove {
			if start >= 2 && text[start-2:start] == "\n\n" {
				start -= 2
			}
			if end < len(text) && text[end] == '\n' {
				end++
			}
			return []byte(text[:start] + text[end:]), nil
		}
		return []byte(text[:start] + instructionBlock(skillPath) + text[end:]), nil
	})
}
