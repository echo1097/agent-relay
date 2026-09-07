package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"agent-relay/internal/config"
	"agent-relay/internal/discovery"
	"agent-relay/internal/tailscale"
)

func listenAddress(ctx context.Context, cfg config.Config, client tailscale.Client) (string, error) {
	if err := cfg.Validate(); err != nil {
		return "", err
	}
	if cfg.Network.Development {
		return net.JoinHostPort(cfg.Network.BindAddress, strconv.Itoa(cfg.Network.Port)), nil
	}
	status, err := client.Status(ctx)
	if err != nil {
		return "", err
	}
	if !status.Connected || !tailscale.IsIP(status.IP) {
		return "", errors.New("Tailscale is not connected with an IPv4 address; run tailscale up")
	}
	return net.JoinHostPort(status.IP, strconv.Itoa(cfg.Network.Port)), nil
}

func showPeers(output io.Writer, snapshot discovery.Snapshot, running bool, interval int) error {
	if _, err := fmt.Fprintf(output, "\nPeers\n  Cache refreshed: %s\n", formatTime(snapshot.UpdatedAt)); err != nil {
		return err
	}
	if snapshot.Error != "" {
		if _, err := fmt.Fprintf(output, "  Discovery: %s\n", snapshot.Error); err != nil {
			return err
		}
	}
	stale := !running || snapshot.UpdatedAt.IsZero() || time.Since(snapshot.UpdatedAt)-15*time.Second > time.Duration(interval)*time.Second
	if stale {
		if _, err := fmt.Fprintln(output, "  Cache is stale; start the daemon and wait for discovery."); err != nil {
			return err
		}
	}
	if len(snapshot.Peers) == 0 {
		_, err := fmt.Fprintln(output, "  No peers discovered.")
		return err
	}
	for _, peer := range snapshot.Peers {
		state := peer.State
		if stale {
			state = "stale (" + state + ")"
		}
		if _, err := fmt.Fprintf(output, "  %s  %s  %s  %s  last seen: %s\n", peer.Node.Name, peer.Node.ID, peer.IP, state, formatTime(peer.LastSeen)); err != nil {
			return err
		}
		if peer.Error != "" {
			if _, err := fmt.Fprintf(output, "    %s\n", peer.Error); err != nil {
				return err
			}
		}
	}
	return nil
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return "never"
	}
	return value.UTC().Format(time.RFC3339)
}

func showTailscale(ctx context.Context, output io.Writer, client tailscale.Client) (tailscale.Status, error) {
	status, statusErr := client.Status(ctx)
	_, err := fmt.Fprintf(output, "\nTailscale\n  Installed: %t\n  Running: %t\n  Connected: %t\n  IPv4: %s\n  Visible peers: %d\n", status.Installed, status.Running, status.Connected, status.IP, len(status.Peers))
	if err != nil {
		return status, err
	}
	if statusErr != nil {
		_, err = fmt.Fprintf(output, "  %s\n", statusErr)
	} else if !status.Connected {
		_, err = fmt.Fprintln(output, "  Connect Tailscale with tailscale up.")
	}
	return status, err
}

func runDoctor(ctx context.Context, output io.Writer, cfg config.Config, client tailscale.Client, prober discovery.Prober, localID string, running bool) error {
	status, err := showTailscale(ctx, output, client)
	if err != nil {
		return err
	}
	if !status.Connected {
		return errors.New("doctor: Tailscale is not ready")
	}
	address, err := listenAddress(ctx, cfg, client)
	if err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	hello, probeErr := prober.Hello(ctx, host, cfg.Network.Port)
	localReady := running && probeErr == nil && hello.Node.ID == localID
	if _, err := fmt.Fprintf(output, "\nLocal listener\n  %s: %t\n", address, localReady); err != nil {
		return err
	}
	if !localReady {
		if _, err := fmt.Fprintln(output, "  Start or restart agent-relay daemon and check network configuration."); err != nil {
			return err
		}
	}
	manager := discovery.Manager{Client: client, Prober: prober, Port: cfg.Network.Port, LocalID: localID}
	snapshot, refreshErr := manager.Refresh(ctx)
	if err := showPeers(output, snapshot, true, cfg.Discovery.IntervalSeconds); err != nil {
		return err
	}
	reachable := 0
	for _, peer := range snapshot.Peers {
		if peer.State == "online" {
			reachable++
		}
	}
	if reachable == 0 {
		if _, err := fmt.Fprintln(output, "  Start Agent Relay on another tailnet device; check matching ports, protocol versions, Tailscale ACLs, and firewalls."); err != nil {
			return err
		}
	}
	if refreshErr != nil {
		return refreshErr
	}
	if !localReady || reachable == 0 {
		return errors.New("doctor: listener or peer reachability checks failed")
	}
	return nil
}
