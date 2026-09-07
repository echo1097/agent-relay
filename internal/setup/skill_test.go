package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBundledSkillMatchesDraft(t *testing.T) {
	data, err := os.ReadFile("../../skills/agent-relay/SKILL.md")
	if err != nil || string(data) != skillContent {
		t.Fatal("update skill_content.go to match skills/agent-relay/SKILL.md", err)
	}
}

func TestSkillPaths(t *testing.T) {
	env := Environment{Home: "/home/person", Getenv: func(string) string { return "" }}
	if SkillPath(env, "codex") != "/home/person/.agents/skills/agent-relay/SKILL.md" || SkillPath(env, "claude") != "/home/person/.claude/skills/agent-relay/SKILL.md" {
		t.Fatal("wrong default paths")
	}
	env.Getenv = func(key string) string {
		return map[string]string{"CODEX_HOME": "/custom/codex", "CLAUDE_CONFIG_DIR": "/custom/claude"}[key]
	}
	if SkillPath(env, "codex") != "/home/person/.agents/skills/agent-relay/SKILL.md" || SkillPath(env, "claude") != "/custom/claude/skills/agent-relay/SKILL.md" {
		t.Fatal("wrong override paths")
	}
}

func TestSkillLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-relay", "SKILL.md")
	for range 2 {
		if _, err := SyncSkill(path, false); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != skillContent {
		t.Fatal("missing bundled skill", err)
	}
	oldData := []byte("older managed release")
	if err := os.WriteFile(path, oldData, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), ".agent-relay-sha256"), []byte(skillHash(oldData)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := SyncSkill(path, false); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if string(data) != skillContent {
		t.Fatal("managed upgrade failed")
	}
	for range 2 {
		if _, err := SyncSkill(path, true); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatal("managed skill directory remains", err)
	}
}

func TestSkillPreservesCustomContent(t *testing.T) {
	for _, managed := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "agent-relay", "SKILL.md")
		if managed {
			if _, err := SyncSkill(path, false); err != nil {
				t.Fatal(err)
			}
		} else if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("my custom skill"), 0600); err != nil {
			t.Fatal(err)
		}
		for _, remove := range []bool{false, true} {
			state, err := SyncSkill(path, remove)
			if err != nil || !strings.Contains(state, "preserved") {
				t.Fatal(state, err)
			}
			data, _ := os.ReadFile(path)
			if string(data) != "my custom skill" {
				t.Fatal("custom content changed")
			}
		}
	}
}

func TestSkillPreservesExtraFilesAndRejectsSymlinks(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "agent-relay", "SKILL.md")
	if _, err := SyncSkill(path, false); err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(filepath.Dir(path), "notes.md")
	if err := os.WriteFile(extra, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := SyncSkill(path, true); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(extra); err != nil || string(data) != "keep" {
		t.Fatal("extra file lost", err)
	}
	if err := os.Symlink(extra, path); err != nil {
		t.Fatal(err)
	}
	for _, remove := range []bool{false, true} {
		if _, err := SyncSkill(path, remove); err == nil {
			t.Fatal("accepted symlink skill")
		}
	}
}

func TestSkillAdoptsIdenticalDraft(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SKILL.md")
	if err := os.WriteFile(path, []byte(skillContent), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := SyncSkill(path, false); err != nil {
		t.Fatal(err)
	}
	if _, err := SyncSkill(path, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("identical draft was not adopted", err)
	}
}
