package storage

import (
	"context"
	"database/sql"
	"errors"
	"net"

	"agent-relay/internal/protocol"
	"agent-relay/internal/tailscale"
)

var ErrNodeNotTrusted = errors.New("NODE_NOT_TRUSTED: peer trust was revoked before persistence")

type TrustState string

const (
	Unknown TrustState = "unknown"
	Trusted TrustState = "trusted"
	Blocked TrustState = "blocked"
)

type PeerTrust struct {
	NodeID      string
	Name        string
	State       TrustState
	TailscaleID string
	Address     string
	Port        int
	Development bool
}

func (store *Store) PeerTrust(ctx context.Context, nodeID string) (PeerTrust, error) {
	peer := PeerTrust{NodeID: nodeID, State: Unknown}
	err := store.db.QueryRowContext(ctx, `SELECT node_id, name, state, tailscale_id, address, port, development FROM peer_trust WHERE node_id = ?`, nodeID).Scan(&peer.NodeID, &peer.Name, &peer.State, &peer.TailscaleID, &peer.Address, &peer.Port, &peer.Development)
	if errors.Is(err, sql.ErrNoRows) {
		return peer, nil
	}
	return peer, err
}

func (store *Store) ListPeerTrust(ctx context.Context) ([]PeerTrust, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT node_id, name, state, tailscale_id, address, port, development FROM peer_trust ORDER BY name, node_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	peers := []PeerTrust{}
	for rows.Next() {
		var peer PeerTrust
		if err := rows.Scan(&peer.NodeID, &peer.Name, &peer.State, &peer.TailscaleID, &peer.Address, &peer.Port, &peer.Development); err != nil {
			return nil, err
		}
		peers = append(peers, peer)
	}
	return peers, rows.Err()
}

func (store *Store) SetPeerTrust(ctx context.Context, peer PeerTrust) error {
	if err := (protocol.Node{ID: peer.NodeID, Name: peer.Name}).Validate(); err != nil {
		return err
	}
	if peer.State != Unknown && peer.State != Trusted && peer.State != Blocked {
		return errors.New("invalid peer trust state")
	}
	if peer.Port == 0 {
		peer.Port = 47832
	}
	if peer.State == Trusted {
		if peer.Development {
			address := net.ParseIP(peer.Address)
			if address == nil || !address.IsLoopback() {
				return errors.New("development trust requires a loopback address")
			}
		} else if peer.TailscaleID == "" || !tailscale.IsIP(peer.Address) {
			return errors.New("trust requires a stable Tailscale device identity and address")
		}
	}
	result, err := store.db.ExecContext(ctx, `INSERT INTO peer_trust (node_id, name, state, tailscale_id, address, port, development)
 SELECT ?, ?, ?, ?, ?, ?, ? WHERE NOT EXISTS (SELECT 1 FROM local_node WHERE node_id = ?)
 ON CONFLICT(node_id) DO UPDATE SET name = excluded.name, state = excluded.state, tailscale_id = excluded.tailscale_id, address = excluded.address, port = excluded.port, development = excluded.development`, peer.NodeID, peer.Name, peer.State, peer.TailscaleID, peer.Address, peer.Port, peer.Development, peer.NodeID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count == 0 {
		return errors.New("cannot change peer trust for the local node")
	}
	return err
}

func (store *Store) ObservePeer(ctx context.Context, node protocol.Node) error {
	if err := node.Validate(); err != nil {
		return err
	}
	_, err := store.db.ExecContext(ctx, `INSERT INTO peer_trust (node_id, name, state)
 SELECT ?, ?, 'unknown' WHERE NOT EXISTS (SELECT 1 FROM local_node WHERE node_id = ?)
 ON CONFLICT(node_id) DO NOTHING`, node.ID, node.Name, node.ID)
	return err
}

func (store *Store) TrustTailnetPeer(ctx context.Context, node protocol.Node, deviceID, address string, port int) error {
	if err := node.Validate(); err != nil {
		return err
	}
	if deviceID == "" || !tailscale.IsIP(address) || port < 1 || port > 65535 {
		return errors.New("automatic trust requires a verified Tailscale device")
	}
	_, err := store.db.ExecContext(ctx, `INSERT INTO peer_trust (node_id, name, state, tailscale_id, address, port)
 SELECT ?, ?, 'trusted', ?, ?, ? WHERE NOT EXISTS (SELECT 1 FROM local_node WHERE node_id = ?)
 ON CONFLICT(node_id) DO UPDATE SET name = excluded.name, state = 'trusted', tailscale_id = excluded.tailscale_id, address = excluded.address, port = excluded.port
 WHERE peer_trust.state != 'blocked' AND peer_trust.development = 0 AND (peer_trust.tailscale_id = '' OR peer_trust.tailscale_id = excluded.tailscale_id)`, node.ID, node.Name, deviceID, address, port, node.ID)
	return err
}
