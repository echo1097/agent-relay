package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"os/exec"
	"time"
)

type Peer struct {
	ID   string
	Name string
	IP   string
}

type Status struct {
	Installed bool
	Running   bool
	Connected bool
	IP        string
	Peers     []Peer
}

type Client interface {
	Status(context.Context) (Status, error)
}

type CLI struct {
	LookPath func(string) (string, error)
	Run      func(context.Context, string, ...string) ([]byte, error)
}

func New() *CLI {
	return &CLI{LookPath: exec.LookPath, Run: func(ctx context.Context, path string, args ...string) ([]byte, error) {
		command := exec.CommandContext(ctx, path, args...)
		command.WaitDelay = time.Second
		return command.Output()
	}}
}

func IsIP(value string) bool {
	address, err := netip.ParseAddr(value)
	return err == nil && address.Is4() && netip.MustParsePrefix("100.64.0.0/10").Contains(address)
}

func ipv4(addresses []string) string {
	for _, address := range addresses {
		if IsIP(address) {
			return address
		}
	}
	return ""
}

func (client *CLI) Status(ctx context.Context) (Status, error) {
	var status Status
	path, err := client.LookPath("tailscale")
	if err != nil {
		path, err = client.LookPath("/Applications/Tailscale.app/Contents/MacOS/Tailscale")
	}
	if err != nil {
		return status, errors.New("Tailscale not installed; install Tailscale and make its CLI available")
	}
	status.Installed = true
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	data, err := client.Run(ctx, path, "status", "--json")
	if err != nil {
		return status, errors.New("Tailscale status unavailable; start Tailscale and check tailscale status")
	}
	var raw struct {
		BackendState string
		TailscaleIPs []string
		Self         *struct {
			ID     string
			Online bool
		}
		Peer map[string]struct {
			ID           string
			HostName     string
			TailscaleIPs []string
		}
	}
	if err := json.Unmarshal(data, &raw); err != nil || raw.BackendState == "" {
		return status, errors.New("malformed Tailscale status response")
	}
	status.Running = true
	status.IP = ipv4(raw.TailscaleIPs)
	status.Connected = raw.BackendState == "Running" && raw.Self != nil && raw.Self.Online && status.IP != ""
	for key, peer := range raw.Peer {
		peerIP := ipv4(peer.TailscaleIPs)
		if peerIP == "" || peerIP == status.IP || (raw.Self != nil && peer.ID == raw.Self.ID) {
			continue
		}
		if peer.ID == "" {
			peer.ID = key
		}
		status.Peers = append(status.Peers, Peer{ID: peer.ID, Name: peer.HostName, IP: peerIP})
	}
	return status, nil
}
