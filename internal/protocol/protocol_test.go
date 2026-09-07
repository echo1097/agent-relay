package protocol

import (
	"encoding/json"
	"strings"
	"testing"

	"agent-relay/internal/agents"
)

const testNodeID = "node_019a84fc-1b72-7000-8000-000000000001"
const testAgentID = "agent_019a84fc-1b72-7000-8000-000000000002"

func TestPublicAgentProjection(t *testing.T) {
	localAgent := agents.Agent{ID: testAgentID, NodeID: testNodeID, DisplayName: "codex-auth", Provider: "codex", Status: agents.Busy, Metadata: agents.Metadata{Task: "Investigating auth", Project: "backend", Repository: "https://github.com/example/backend.git", Branch: "fix/auth", Cwd: "/Users/private/backend", Files: []string{"/Users/private/secret.go", "relative.go"}}}
	publicAgent := PublicAgent(localAgent)
	if err := publicAgent.Validate(); err != nil {
		t.Fatal(err)
	}
	if publicAgent.Task != localAgent.Task || publicAgent.Repository != "github.com/example/backend" || publicAgent.Branch != "fix/auth" {
		t.Fatalf("lost safe fields: %+v", publicAgent)
	}
	data, err := json.Marshal(publicAgent)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"cwd", "files", "Users", "secret.go", "relative.go", "node_id", "registered_at"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("exposed %q: %s", forbidden, data)
		}
	}
}

func TestUnsafeMetadataIsOmitted(t *testing.T) {
	for _, value := range []string{
		"/Users/person/private", "Working in /home/person/repo", `C:\Users\person\private`, `Opening C:/Users/private`, `\\server\share`, "~/private", "file:///home/person/private", "API_KEY=private-value", "password: private-value", "Bearer private-value", "${HOME}", "$SECRET_TOKEN", "%USERPROFILE%", "https://user:pass@example.com/repo", "https://example.com/repo?token=private", "https://example.com/repo#private", "sk-secretvalue", "github_pat_private", "line\nvalue", "secret\x00value", strings.Repeat("x", 2049),
	} {
		t.Run(value, func(t *testing.T) {
			agent := PublicAgent(agents.Agent{ID: testAgentID, Status: agents.Online, DisplayName: value, Provider: value, Metadata: agents.Metadata{Task: value, Project: value, Repository: value, Branch: value}})
			if agent.DisplayName != "agent" || agent.Provider != "" || agent.Task != "" || agent.Project != "" || agent.Repository != "" || agent.Branch != "" {
				t.Fatalf("unsafe projection: %+v", agent)
			}
			if err := agent.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestValidation(t *testing.T) {
	node := PublicNode(testNodeID, "test-node")
	hello := Hello{Protocol: Name, ProtocolVersion: Version, Node: node, Version: "0.1.0-dev"}
	if err := hello.Validate(); err != nil {
		t.Fatal(err)
	}
	agent := PublicAgent(agents.Agent{ID: testAgentID, DisplayName: "agent", Status: agents.Online})
	for _, invalid := range []interface{ Validate() error }{
		Node{ID: "invalid", Name: "name"},
		Node{ID: testNodeID, Name: "/private/name"},
		Hello{Protocol: Name, ProtocolVersion: 2, Node: node, Version: "test"},
		Health{ProtocolVersion: Version, Status: "invalid"},
		Agent{ID: testAgentID, DisplayName: "agent", Status: "invalid"},
		Agent{ID: "invalid", DisplayName: "agent", Status: agents.Online},
		AgentList{ProtocolVersion: Version},
		AgentList{ProtocolVersion: Version, Agents: []Agent{agent, agent}},
		Error{ProtocolVersion: Version, Error: ErrorDetail{Code: "invalid", Message: "error"}},
	} {
		if err := invalid.Validate(); err == nil {
			t.Fatalf("accepted invalid object: %+v", invalid)
		}
	}
	if err := (AgentList{ProtocolVersion: Version, Agents: []Agent{}}).Validate(); err != nil {
		t.Fatal(err)
	}
}
