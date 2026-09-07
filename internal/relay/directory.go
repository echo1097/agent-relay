package relay

import (
	"context"
	"errors"
	"net"
	"sort"
	"sync"
	"time"

	"agent-relay/internal/agents"
	"agent-relay/internal/discovery"
	"agent-relay/internal/protocol"
	"agent-relay/internal/storage"
	"agent-relay/internal/tailscale"
)

type PeerProber interface {
	discovery.Prober
	Agents(context.Context, string, int) ([]protocol.Agent, error)
}

type Agent struct {
	protocol.Agent
	Node  protocol.Node      `json:"node"`
	Trust storage.TrustState `json:"trust"`
}

type AgentList struct {
	Agents      []Agent  `json:"agents"`
	Unavailable []string `json:"unavailable_nodes"`
}

type Directory struct {
	Registry    *agents.Registry
	Store       *storage.Store
	Node        protocol.Node
	Path        string
	Port        int
	Development bool
	Tailscale   tailscale.Client
	Prober      PeerProber
	mutex       sync.Mutex
	known       map[string]Agent
}

func (directory *Directory) List(ctx context.Context) (AgentList, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	result := AgentList{Agents: []Agent{}, Unavailable: []string{}}
	localAgents, err := directory.Registry.List(ctx)
	if err != nil {
		return result, err
	}
	for _, agent := range localAgents {
		result.Agents = append(result.Agents, Agent{Agent: protocol.PublicAgent(agent), Node: directory.Node, Trust: storage.Trusted})
	}
	snapshot, err := discovery.Read(directory.Path)
	if err != nil {
		return result, err
	}
	peers, err := directory.Store.ListPeerTrust(ctx)
	if err != nil {
		return result, err
	}
	candidates := make(map[string]storage.PeerTrust)
	for _, peer := range snapshot.Peers {
		if peer.Node.Validate() == nil && peer.Node.ID != directory.Node.ID {
			candidates[peer.Node.ID] = storage.PeerTrust{NodeID: peer.Node.ID, Name: peer.Node.Name, TailscaleID: peer.TailscaleID, Address: peer.IP, Port: directory.Port}
		}
	}
	for _, peer := range peers {
		if peer.State == storage.Trusted || peer.State == storage.Blocked {
			candidates[peer.NodeID] = peer
		}
	}
	var tailStatus tailscale.Status
	if !directory.Development && len(candidates) > 0 {
		tailStatus, err = directory.Tailscale.Status(ctx)
		if err != nil || !tailStatus.Connected {
			for nodeID := range candidates {
				result.Unavailable = append(result.Unavailable, nodeID)
			}
			return result, nil
		}
	}
	for nodeID, peer := range candidates {
		if peer.State == storage.Blocked {
			continue
		}
		address := peer.Address
		if !directory.Development {
			address = ""
			for _, device := range tailStatus.Peers {
				if device.ID == peer.TailscaleID && tailscale.IsIP(device.IP) {
					address = device.IP
					break
				}
			}
		} else if ip := net.ParseIP(address); ip == nil || !ip.IsLoopback() {
			address = ""
		}
		if address == "" {
			result.Unavailable = append(result.Unavailable, nodeID)
			continue
		}
		hello, probeErr := directory.Prober.Hello(ctx, address, peer.Port)
		if probeErr != nil || hello.Validate() != nil || hello.Node.ID != nodeID {
			result.Unavailable = append(result.Unavailable, nodeID)
			continue
		}
		remoteAgents, probeErr := directory.Prober.Agents(ctx, address, peer.Port)
		if probeErr != nil {
			result.Unavailable = append(result.Unavailable, nodeID)
			continue
		}
		trust, trustErr := directory.Store.PeerTrust(ctx, nodeID)
		if trustErr != nil {
			return result, trustErr
		}
		if trust.State == storage.Blocked {
			continue
		}
		for _, agent := range remoteAgents {
			if agent.Validate() != nil {
				return result, discovery.ErrMalformed
			}
			result.Agents = append(result.Agents, Agent{Agent: agent, Node: hello.Node, Trust: trust.State})
		}
	}
	seen := make(map[string]bool)
	for _, agent := range result.Agents {
		if seen[agent.ID] {
			return AgentList{}, errors.New("ambiguous agent ID advertised by multiple nodes")
		}
		seen[agent.ID] = true
	}
	sort.Slice(result.Agents, func(left, right int) bool { return result.Agents[left].ID < result.Agents[right].ID })
	sort.Strings(result.Unavailable)
	directory.mutex.Lock()
	if directory.known == nil {
		directory.known = make(map[string]Agent)
	}
	for _, agent := range result.Agents {
		if previous, ok := directory.known[agent.ID]; ok && previous.Node.ID != agent.Node.ID {
			directory.mutex.Unlock()
			return AgentList{}, errors.New("agent changed node identity")
		}
	}
	for _, agent := range result.Agents {
		directory.known[agent.ID] = agent
	}
	directory.mutex.Unlock()
	return result, nil
}

func (directory *Directory) Get(ctx context.Context, agentID string) (Agent, error) {
	result, err := directory.List(ctx)
	if err != nil {
		return Agent{}, err
	}
	for _, agent := range result.Agents {
		if agent.ID == agentID {
			return agent, nil
		}
	}
	return Agent{}, agents.ErrNotFound
}

func (directory *Directory) Route(ctx context.Context, agentID string) (string, error) {
	directory.mutex.Lock()
	agent, known := directory.known[agentID]
	directory.mutex.Unlock()
	if !known {
		var err error
		agent, err = directory.Get(ctx, agentID)
		if err != nil {
			return "", err
		}
	}
	if agent.Node.ID == directory.Node.ID {
		return "", errors.New("V0.1 messaging requires an agent on a remote node")
	}
	return agent.Node.ID, nil
}
