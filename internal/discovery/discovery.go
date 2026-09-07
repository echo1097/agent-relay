package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"agent-relay/internal/protocol"
	"agent-relay/internal/storage"
	"agent-relay/internal/tailscale"
)

type Peer struct {
	TailscaleID string        `json:"tailscale_id"`
	IP          string        `json:"ip"`
	Node        protocol.Node `json:"node"`
	Version     string        `json:"version"`
	State       string        `json:"state"`
	LastSeen    time.Time     `json:"last_seen"`
	Failures    int           `json:"failures"`
	Error       string        `json:"error,omitempty"`
}

type Snapshot struct {
	UpdatedAt time.Time `json:"updated_at"`
	Error     string    `json:"error,omitempty"`
	Peers     []Peer    `json:"peers"`
}

type Manager struct {
	Store    *storage.Store
	Client   tailscale.Client
	Prober   Prober
	Port     int
	LocalID  string
	BoundIP  string
	Path     string
	Logger   *slog.Logger
	mutex    sync.Mutex
	snapshot Snapshot
}

func Read(path string) (Snapshot, error) {
	var snapshot Snapshot
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return snapshot, nil
	}
	if err != nil {
		return snapshot, err
	}
	err = json.Unmarshal(data, &snapshot)
	return snapshot, err
}

func (manager *Manager) save() error {
	data, err := json.Marshal(manager.snapshot)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(manager.Path), ".peers-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	return os.Rename(file.Name(), manager.Path)
}

func (manager *Manager) Refresh(ctx context.Context) (Snapshot, error) {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	if manager.snapshot.UpdatedAt.IsZero() && manager.Path != "" {
		cached, err := Read(manager.Path)
		if err != nil {
			if manager.Logger != nil {
				manager.Logger.Warn("discovery cache unreadable; rebuilding from live peers")
			}
		} else {
			manager.snapshot = cached
		}
	}
	roundCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	status, statusErr := manager.Client.Status(roundCtx)
	if statusErr == nil && !status.Connected {
		statusErr = errors.New("Tailscale disconnected; run tailscale up")
	}
	if statusErr == nil && manager.BoundIP != "" && manager.BoundIP != status.IP {
		statusErr = errors.New("Tailscale IP changed; restart the Agent Relay daemon")
	}
	previous := make(map[string]Peer)
	for _, peer := range manager.snapshot.Peers {
		previous[peer.TailscaleID] = peer
	}
	next := Snapshot{UpdatedAt: time.Now().UTC(), Peers: []Peer{}}
	if statusErr != nil {
		next.Error = statusErr.Error()
	}
	type result struct {
		peer  tailscale.Peer
		hello protocol.Hello
		err   error
	}
	results := make(chan result, len(status.Peers))
	jobs := make(chan tailscale.Peer, len(status.Peers))
	visible := make(map[string]bool)
	if statusErr == nil {
		for _, peer := range status.Peers {
			if peer.ID == "" || visible[peer.ID] || !tailscale.IsIP(peer.IP) || peer.IP == status.IP {
				continue
			}
			visible[peer.ID] = true
			jobs <- peer
		}
	}
	close(jobs)
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for peer := range jobs {
				if roundCtx.Err() != nil {
					results <- result{peer: peer, err: roundCtx.Err()}
					continue
				}
				hello, err := manager.Prober.Hello(roundCtx, peer.IP, manager.Port)
				if err == nil && (hello.Validate() != nil || hello.Node.ID == manager.LocalID) {
					err = ErrMalformed
				}
				results <- result{peer: peer, hello: hello, err: err}
			}
		}()
	}
	workers.Wait()
	close(results)
	persistCtx, stopPersist := context.WithTimeout(ctx, 5*time.Second)
	defer stopPersist()
	for probe := range results {
		peer := previous[probe.peer.ID]
		oldState := peer.State
		peer.TailscaleID = probe.peer.ID
		peer.IP = probe.peer.IP
		if probe.err == nil {
			if manager.Store != nil {
				if err := manager.Store.ObservePeer(persistCtx, probe.hello.Node); err != nil {
					return next, err
				}
			}
			peer.Node = probe.hello.Node
			peer.Version = probe.hello.Version
			peer.LastSeen = time.Now().UTC()
			peer.State = "online"
			peer.Failures = 0
			peer.Error = ""
		} else {
			peer.Failures++
			peer.Error = probe.err.Error()
			peer.State = "unreachable"
			if peer.Failures < 3 && !peer.LastSeen.IsZero() {
				peer.State = "suspect"
			}
			if errors.Is(probe.err, ErrIncompatible) {
				peer.State = "incompatible"
			}
			if errors.Is(probe.err, ErrMalformed) {
				peer.State = "invalid"
			}
		}
		if manager.Logger != nil && peer.State != oldState {
			event := "peer lost"
			if peer.State == "online" {
				event = "peer discovered"
			}
			manager.Logger.Info(event, "peer_id", peer.Node.ID, "ip", peer.IP, "state", peer.State)
		}
		next.Peers = append(next.Peers, peer)
	}
	for id, peer := range previous {
		if visible[id] {
			continue
		}
		oldState := peer.State
		peer.State = "disappeared"
		peer.Error = "peer no longer visible in Tailscale"
		if statusErr != nil {
			peer.State = "unknown"
			peer.Error = next.Error
		}
		if manager.Logger != nil && peer.State != oldState {
			manager.Logger.Info("peer lost", "peer_id", peer.Node.ID, "state", peer.State)
		}
		next.Peers = append(next.Peers, peer)
	}
	sort.Slice(next.Peers, func(left, right int) bool { return next.Peers[left].TailscaleID < next.Peers[right].TailscaleID })
	manager.snapshot = next
	if manager.Path != "" {
		if err := manager.save(); err != nil {
			return next, err
		}
	}
	return next, statusErr
}

func (manager *Manager) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if _, err := manager.Refresh(ctx); err != nil && ctx.Err() == nil && manager.Logger != nil {
			manager.Logger.Warn("discovery refresh failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
