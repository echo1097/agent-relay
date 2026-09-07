package transport

import (
	"context"
	"errors"
	"fmt"
	"net"

	"agent-relay/internal/protocol"
	"agent-relay/internal/storage"
	"agent-relay/internal/tailscale"
)

type AuthorizationError struct{ Detail string }

func (err *AuthorizationError) Error() string {
	return protocol.NodeNotTrusted + ": " + err.Detail
}

func (service *Service) trustedPeer(ctx context.Context, nodeID string) (storage.PeerTrust, error) {
	if nodeID == service.NodeID || (protocol.Node{ID: nodeID, Name: "peer"}).Validate() != nil {
		return storage.PeerTrust{}, &AuthorizationError{Detail: "Specify a valid remote Relay node ID."}
	}
	peer, err := service.Store.PeerTrust(ctx, nodeID)
	if err != nil {
		return peer, err
	}
	if peer.State == storage.Blocked {
		return peer, &AuthorizationError{Detail: "This node is blocked. The receiving owner must explicitly trust it to allow communication."}
	}
	if peer.State != storage.Trusted {
		return peer, &AuthorizationError{Detail: "This node is unknown. The receiving owner must run agent-relay trust with the sender node ID."}
	}
	if peer.Development != service.Development {
		return peer, &AuthorizationError{Detail: "The trust binding does not match the network mode. Trust the peer again in the current mode."}
	}
	return peer, nil
}

func (service *Service) peerAddress(ctx context.Context, peer storage.PeerTrust) (string, error) {
	if service.Tailscale == nil || peer.TailscaleID == "" {
		return "", errors.New("Tailscale identity unavailable")
	}
	status, err := service.Tailscale.Status(ctx)
	if err != nil {
		return "", err
	}
	if !status.Connected {
		return "", errors.New("Tailscale is disconnected")
	}
	for _, candidate := range status.Peers {
		if candidate.ID == peer.TailscaleID && tailscale.IsIP(candidate.IP) {
			return candidate.IP, nil
		}
	}
	return "", errors.New("trusted Tailscale device is not visible")
}

func (service *Service) Authorize(ctx context.Context, nodeID, remoteAddress string) error {
	peer, err := service.trustedPeer(ctx, nodeID)
	if err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(remoteAddress)
	if err != nil {
		return &AuthorizationError{Detail: "The connection source is invalid."}
	}
	expected := peer.Address
	if !service.Development {
		expected, err = service.peerAddress(ctx, peer)
		if err != nil {
			return &AuthorizationError{Detail: "The trusted Tailscale device cannot be verified. Check Tailscale connectivity and the peer binding."}
		}
	}
	sourceIP := net.ParseIP(host)
	if sourceIP == nil || !sourceIP.Equal(net.ParseIP(expected)) || (service.Development && !sourceIP.IsLoopback()) {
		return &AuthorizationError{Detail: fmt.Sprintf("The connection does not belong to the trusted device for %s.", nodeID)}
	}
	return nil
}
