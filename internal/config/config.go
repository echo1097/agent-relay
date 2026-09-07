package config

import (
	"agent-relay/internal/protocol"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type TrustedPeer struct {
	NodeID  string `toml:"node_id"`
	Address string `toml:"address"`
	Port    int    `toml:"port"`
}

type Config struct {
	TrustedPeers []TrustedPeer `toml:"trusted_peers"`
	Network      struct {
		Development bool   `toml:"development"`
		Port        int    `toml:"port"`
		BindAddress string `toml:"bind_address"`
	} `toml:"network"`
	Discovery struct {
		IntervalSeconds int `toml:"interval_seconds"`
	} `toml:"discovery"`
	Presence struct {
		HeartbeatSeconds    int `toml:"heartbeat_seconds"`
		OfflineAfterSeconds int `toml:"offline_after_seconds"`
	} `toml:"presence"`
	Messages struct {
		RequestExpirationHours int `toml:"request_expiration_hours"`
		RetryIntervalSeconds   int `toml:"retry_interval_seconds"`
	} `toml:"messages"`
	Logging struct {
		Level string `toml:"level"`
	} `toml:"logging"`
}

type Paths struct {
	Home     string
	Config   string
	Database string
	Logs     string
	Lock     string
}

func Resolve(home string) (Paths, error) {
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, err
		}
		home = filepath.Join(userHome, ".agent-relay")
	}
	home, err := filepath.Abs(home)
	if err != nil {
		return Paths{}, err
	}
	return Paths{Home: home, Config: filepath.Join(home, "config.toml"), Database: filepath.Join(home, "relay.db"), Logs: filepath.Join(home, "logs"), Lock: filepath.Join(home, "daemon.lock")}, nil
}

func (paths Paths) Ensure() error {
	for _, dir := range []string{paths.Home, paths.Logs} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}
	return nil
}

func Defaults() Config {
	var cfg Config
	cfg.Network.Port = 47832
	cfg.Network.BindAddress = "tailscale"
	cfg.Discovery.IntervalSeconds = 15
	cfg.Presence.HeartbeatSeconds = 10
	cfg.Presence.OfflineAfterSeconds = 30
	cfg.Messages.RequestExpirationHours = 24
	cfg.Messages.RetryIntervalSeconds = 900
	cfg.Logging.Level = "info"
	return cfg
}

func Load(path string) (Config, error) {
	cfg := Defaults()
	configData, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	metadata, err := toml.Decode(string(configData), &cfg)
	if err != nil {
		return Config{}, errors.New("invalid TOML configuration")
	}
	if len(metadata.Undecoded()) > 0 {
		return Config{}, errors.New("configuration contains unknown keys")
	}
	cfg.Logging.Level = strings.ToLower(cfg.Logging.Level)
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (cfg Config) Validate() error {
	seen := map[string]bool{}
	addresses := map[string]bool{}
	for _, peer := range cfg.TrustedPeers {
		address, err := netip.ParseAddr(peer.Address)
		if err != nil || peer.Port < 1 || peer.Port > 65535 || protocol.PublicNode(peer.NodeID, "peer").Validate() != nil || seen[peer.NodeID] || addresses[peer.Address] {
			return errors.New("trusted peers require unique valid node IDs, addresses and ports")
		}
		if cfg.Network.Development && !address.IsLoopback() || !cfg.Network.Development && !netip.MustParsePrefix("100.64.0.0/10").Contains(address) {
			return errors.New("trusted peer addresses must be Tailscale IPv4, or loopback in development")
		}
		seen[peer.NodeID], addresses[peer.Address] = true, true
	}
	if cfg.Messages.RequestExpirationHours < 1 || int64(cfg.Messages.RequestExpirationHours) > int64((1<<63-1)/time.Hour) || cfg.Messages.RetryIntervalSeconds < 300 || int64(cfg.Messages.RetryIntervalSeconds) > int64((1<<63-1)/time.Second) {
		return errors.New("invalid message expiration or retry interval (minimum 300 seconds)")
	}
	if cfg.Network.Development {
		address, err := netip.ParseAddr(cfg.Network.BindAddress)
		if err != nil || !address.IsLoopback() {
			return errors.New("development mode requires a loopback bind_address")
		}
	} else if cfg.Network.BindAddress != "tailscale" {
		return errors.New("production network.bind_address must be tailscale")
	}
	if cfg.Network.Port < 1 || cfg.Network.Port > 65535 {
		return errors.New("network.port must be between 1 and 65535")
	}
	if cfg.Discovery.IntervalSeconds <= 0 || cfg.Presence.HeartbeatSeconds <= 0 || cfg.Messages.RequestExpirationHours <= 0 {
		return errors.New("configuration intervals must be positive")
	}
	if cfg.Presence.OfflineAfterSeconds <= cfg.Presence.HeartbeatSeconds {
		return errors.New("presence.offline_after_seconds must exceed heartbeat_seconds")
	}
	if int64(cfg.Discovery.IntervalSeconds) > int64((1<<63-1)/time.Second) {
		return errors.New("discovery.interval_seconds is too large")
	}
	if int64(cfg.Presence.OfflineAfterSeconds) > int64((1<<63-1)/time.Second) {
		return errors.New("presence.offline_after_seconds is too large")
	}
	switch cfg.Logging.Level {
	case "debug", "info", "warn", "error":
		return nil
	default:
		return errors.New("logging.level must be debug, info, warn, or error")
	}
}
