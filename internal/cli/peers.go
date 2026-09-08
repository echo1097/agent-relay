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

func formatRelativeTime(value time.Time) string {
	if value.IsZero() {
		return "unknown"
	}
	age := time.Since(value)
	if age < 0 || age < time.Minute {
		return "just now"
	}
	if age < time.Hour {
		minutes := int(age / time.Minute)
		return fmt.Sprintf("%d minute%s ago", minutes, pluralSuffix(minutes))
	}
	if age < 24*time.Hour {
		hours := int(age / time.Hour)
		return fmt.Sprintf("%d hour%s ago", hours, pluralSuffix(hours))
	}
	days := int(age / (24 * time.Hour))
	return fmt.Sprintf("%d day%s ago", days, pluralSuffix(days))
}

func pluralSuffix(value int) string {
	if value == 1 {
		return ""
	}
	return "s"
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
