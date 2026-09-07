package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"

	"agent-relay/internal/config"
	"agent-relay/internal/discovery"
	"agent-relay/internal/protocol"
	"agent-relay/internal/storage"
	"agent-relay/internal/tailscale"
)

func resolveTrustPeer(ctx context.Context, store *storage.Store, cfg config.Config, snapshot discovery.Snapshot, value string) (storage.PeerTrust, error) {
	if net.ParseIP(value) != nil {
		return storage.PeerTrust{}, errors.New("use a stable Relay node ID or unique peer name, not an IP address")
	}
	savedPeers, err := store.ListPeerTrust(ctx)
	if err != nil {
		return storage.PeerTrust{}, err
	}
	candidates := make(map[string]storage.PeerTrust)
	for _, peer := range savedPeers {
		candidates[peer.NodeID] = peer
	}
	for _, route := range cfg.TrustedPeers {
		peer := candidates[route.NodeID]
		if peer.NodeID == "" {
			peer = storage.PeerTrust{NodeID: route.NodeID, Name: "peer", State: storage.Unknown}
		}
		if peer.Address == "" {
			peer.Address, peer.Port = route.Address, route.Port
		}
		candidates[route.NodeID] = peer
	}
	seenDevices := make(map[string]string)
	for _, discovered := range snapshot.Peers {
		if discovered.Node.Validate() != nil {
			continue
		}
		if device, exists := seenDevices[discovered.Node.ID]; exists && device != discovered.TailscaleID && (value == discovered.Node.ID || value == discovered.Node.Name) {
			return storage.PeerTrust{}, errors.New("multiple devices advertise this Relay identity; verify the peer identities before trusting")
		}
		seenDevices[discovered.Node.ID] = discovered.TailscaleID
		peer := candidates[discovered.Node.ID]
		if peer.NodeID == "" {
			peer = storage.PeerTrust{NodeID: discovered.Node.ID, State: storage.Unknown}
		}
		peer.Name = discovered.Node.Name
		if peer.State == storage.Unknown || peer.Address == "" {
			peer.Address, peer.Port, peer.TailscaleID = discovered.IP, cfg.Network.Port, discovered.TailscaleID
		}
		candidates[peer.NodeID] = peer
	}
	if peer, exists := candidates[value]; exists {
		return peer, nil
	}
	var matches []storage.PeerTrust
	for _, peer := range candidates {
		if peer.Name == value {
			matches = append(matches, peer)
		}
	}
	if len(matches) > 1 {
		return storage.PeerTrust{}, errors.New("peer name is ambiguous; use the full Relay node ID from agent-relay peers")
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if (protocol.Node{ID: value, Name: "peer"}).Validate() == nil {
		return storage.PeerTrust{NodeID: value, Name: "peer", State: storage.Unknown, Port: cfg.Network.Port}, nil
	}
	return storage.PeerTrust{}, errors.New("peer not found; run agent-relay peers and use a full node ID or unique name")
}

func setTrust(ctx context.Context, store *storage.Store, cfg config.Config, client tailscale.Client, prober discovery.Prober, peer storage.PeerTrust, state storage.TrustState) (storage.PeerTrust, error) {
	if state == storage.Trusted {
		if cfg.Network.Development {
			address := net.ParseIP(peer.Address)
			if address == nil || !address.IsLoopback() {
				return peer, errors.New("development peer needs an explicit loopback route in trusted_peers")
			}
			peer.Development = true
			peer.TailscaleID = ""
		} else {
			status, err := client.Status(ctx)
			if err != nil {
				return peer, err
			}
			if !status.Connected {
				return peer, errors.New("connect Tailscale before trusting a peer")
			}
			found := false
			for _, candidate := range status.Peers {
				if (peer.TailscaleID != "" && candidate.ID == peer.TailscaleID) || (peer.TailscaleID == "" && candidate.IP == peer.Address) {
					if candidate.ID == "" || !tailscale.IsIP(candidate.IP) {
						continue
					}
					peer.TailscaleID, peer.Address = candidate.ID, candidate.IP
					found = true
					break
				}
			}
			if !found {
				return peer, errors.New("peer device is not visible in Tailscale; start discovery and check agent-relay peers")
			}
			peer.Development = false
		}
		hello, err := prober.Hello(ctx, peer.Address, peer.Port)
		if err != nil {
			return peer, fmt.Errorf("verify peer before trusting: %w", err)
		}
		if hello.Validate() != nil || hello.Node.ID != peer.NodeID {
			return peer, errors.New("peer Relay identity changed; inspect agent-relay peers before trusting the new node ID")
		}
		peer.Name = hello.Node.Name
	}
	peer.State = state
	return peer, store.SetPeerTrust(ctx, peer)
}

func showTrust(ctx context.Context, output io.Writer, store *storage.Store, snapshot discovery.Snapshot) error {
	for _, peer := range snapshot.Peers {
		if peer.Node.Validate() == nil {
			if err := store.ObservePeer(ctx, peer.Node); err != nil {
				return err
			}
		}
	}
	peers, err := store.ListPeerTrust(ctx)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(output, "\nPeer trust (SQLite)"); err != nil {
		return err
	}
	for _, peer := range peers {
		if _, err := fmt.Fprintf(output, "  %s  %s  %s\n", peer.Name, peer.NodeID, peer.State); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintln(output, "  Unknown and blocked nodes cannot communicate. Trust is local to each machine.\n  Use agent-relay trust NODE_ID, block NODE_ID, or trust-state NODE_ID.\n  Legacy trusted_peers entries provide routes only; run trust once to enroll each peer.")
	return err
}
