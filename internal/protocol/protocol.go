package protocol

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"agent-relay/internal/agents"
	"github.com/google/uuid"
)

const Name = "agent-relay"
const Version = 1
const VersionHeader = "X-Agent-Relay-Protocol-Version"

const (
	InvalidRequest      = "INVALID_REQUEST"
	UnsupportedProtocol = "UNSUPPORTED_PROTOCOL"
	NotFound            = "NOT_FOUND"
	MethodNotAllowed    = "METHOD_NOT_ALLOWED"
	InternalError       = "INTERNAL_ERROR"
	RequestTimeout      = "REQUEST_TIMEOUT"
)

type Node struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Hello struct {
	Protocol        string `json:"protocol"`
	ProtocolVersion int    `json:"protocol_version"`
	Node            Node   `json:"node"`
	Version         string `json:"version"`
}

type Health struct {
	ProtocolVersion int    `json:"protocol_version"`
	Status          string `json:"status"`
}

type Agent struct {
	ID          string        `json:"id"`
	DisplayName string        `json:"display_name"`
	Provider    string        `json:"provider,omitempty"`
	Status      agents.Status `json:"status"`
	Task        string        `json:"task,omitempty"`
	Project     string        `json:"project,omitempty"`
	Repository  string        `json:"repository,omitempty"`
	Branch      string        `json:"branch,omitempty"`
}

type AgentList struct {
	ProtocolVersion int     `json:"protocol_version"`
	Agents          []Agent `json:"agents"`
}

type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Error struct {
	ProtocolVersion int         `json:"protocol_version"`
	Error           ErrorDetail `json:"error"`
}

var pathPattern = regexp.MustCompile(`(?:^|[\s"'=(:])(?:/[^/\s]|\\|[A-Za-z]:[\\/]|~/)`)
var credentialPattern = regexp.MustCompile(`(?i)(?:api[_-]?key|access[_-]?token|password|secret|authorization)\s*[:=]|bearer\s+|-----BEGIN|\b(?:sk-|gh[pousr]_|github_pat_|AKIA)[A-Za-z0-9_-]+`)
var environmentPattern = regexp.MustCompile(`\$\{?[A-Za-z_]|%[A-Za-z_][A-Za-z0-9_]*%|(?:^|\s)[A-Z_][A-Z0-9_]*=`)
var urlPattern = regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://[^\s]+`)
var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(?:/[A-Za-z0-9][A-Za-z0-9._-]*)+$`)

func safeText(value string, limit int) bool {
	if len(value) > limit || !utf8.ValidString(value) || pathPattern.MatchString(value) || credentialPattern.MatchString(value) || environmentPattern.MatchString(value) {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) || unicode.In(char, unicode.Cf) {
			return false
		}
	}
	for _, candidate := range urlPattern.FindAllString(value, -1) {
		parsed, err := url.Parse(candidate)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return false
		}
	}
	return true
}

func publicText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if !safeText(value, limit) {
		return ""
	}
	return value
}

func publicRepository(value string) string {
	value = publicText(value, 512)
	if strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "http://") {
		parsed, err := url.Parse(value)
		if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return ""
		}
		value = parsed.Host + strings.TrimSuffix(parsed.Path, ".git")
	}
	if !repositoryPattern.MatchString(value) {
		return ""
	}
	return value
}

func validID(value, prefix string) bool {
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	parsed, err := uuid.Parse(strings.TrimPrefix(value, prefix))
	return err == nil && parsed.Version() == 7 && parsed.Variant() == uuid.RFC4122 && value == prefix+parsed.String()
}

func PublicNode(id, name string) Node {
	name = publicText(name, 255)
	if name == "" {
		name = "node"
	}
	return Node{ID: id, Name: name}
}

func PublicAgent(localAgent agents.Agent) Agent {
	name := publicText(localAgent.DisplayName, 255)
	if name == "" {
		name = "agent"
	}
	return Agent{ID: localAgent.ID, DisplayName: name, Provider: publicText(localAgent.Provider, 128), Status: localAgent.Status, Task: publicText(localAgent.Task, 2048), Project: publicText(localAgent.Project, 255), Repository: publicRepository(localAgent.Repository), Branch: publicText(localAgent.Branch, 255)}
}

func (node Node) Validate() error {
	if !validID(node.ID, "node_") || strings.TrimSpace(node.Name) == "" || !safeText(node.Name, 255) {
		return errors.New("invalid public node")
	}
	return nil
}

func (hello Hello) Validate() error {
	if hello.Protocol != Name || hello.ProtocolVersion != Version || strings.TrimSpace(hello.Version) == "" || !safeText(hello.Version, 128) {
		return errors.New("invalid hello response")
	}
	return hello.Node.Validate()
}

func (health Health) Validate() error {
	if health.ProtocolVersion != Version || health.Status != "ok" {
		return errors.New("invalid health response")
	}
	return nil
}

func (agent Agent) Validate() error {
	if !validID(agent.ID, "agent_") || strings.TrimSpace(agent.DisplayName) == "" || !agent.Status.Valid() {
		return errors.New("invalid public agent")
	}
	if !safeText(agent.DisplayName, 255) || !safeText(agent.Provider, 128) || !safeText(agent.Task, 2048) || !safeText(agent.Project, 255) || !safeText(agent.Branch, 255) || (agent.Repository != "" && publicRepository(agent.Repository) != agent.Repository) {
		return errors.New("unsafe public metadata")
	}
	return nil
}

func (list AgentList) Validate() error {
	if list.ProtocolVersion != Version || list.Agents == nil {
		return errors.New("invalid agent list")
	}
	seen := make(map[string]bool, len(list.Agents))
	for _, agent := range list.Agents {
		if err := agent.Validate(); err != nil {
			return err
		}
		if seen[agent.ID] {
			return errors.New("duplicate public agent ID")
		}
		seen[agent.ID] = true
	}
	return nil
}

func (response Error) Validate() error {
	if response.ProtocolVersion != Version || response.Error.Message == "" || !safeText(response.Error.Message, 512) {
		return errors.New("invalid error response")
	}
	switch response.Error.Code {
	case NodeNotTrusted, AgentNotFound, MessageConflict, MessageExpired, InvalidRequest, UnsupportedProtocol, NotFound, MethodNotAllowed, InternalError, RequestTimeout:
		return nil
	default:
		return errors.New("invalid error code")
	}
}
