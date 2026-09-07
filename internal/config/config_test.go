package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name, input string
		invalid     bool
	}{
		{"partial", "[logging]\nlevel = 'debug'", false},
		{"syntax", "[logging", true},
		{"unknown", "[logging]\nlevle = 'debug'", true},
		{"port", "[network]\nport = 0", true},
		{"hostname", "[network]\nbind_address = 'localhost'", true},
		{"empty address", "[network]\nbind_address = ''", true},
		{"interval", "[discovery]\ninterval_seconds = -1", true},
		{"presence", "[presence]\noffline_after_seconds = 10", true},
		{"level", "[logging]\nlevel = 'trace'", true},
		{"wrong type", "[network]\nport = 'abc'", true},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(testCase.input), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if (err != nil) != testCase.invalid {
				t.Fatalf("unexpected error: %v", err)
			}
			if !testCase.invalid && (cfg.Network.Port != 47832 || cfg.Logging.Level != "debug") {
				t.Fatalf("unexpected config: %+v", cfg)
			}
		})
	}
}

func TestNetworkConfiguration(t *testing.T) {
	if Defaults().Network.BindAddress != "tailscale" {
		t.Fatal("default must be tailscale")
	}
	for _, address := range []string{"127.0.0.1", "::1"} {
		path := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(path, []byte("[network]\ndevelopment = true\nbind_address = '"+address+"'\nport = 47833\n"), 0600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if err != nil || cfg.Network.BindAddress != address || cfg.Network.Port != 47833 {
			t.Fatalf("network config: %+v, %v", cfg.Network, err)
		}
	}
}

func TestDefaultsAndDirectories(t *testing.T) {
	paths, err := Resolve(filepath.Join(t.TempDir(), "relay"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(paths.Config)
	if err != nil || !reflect.DeepEqual(cfg, Defaults()) {
		t.Fatalf("defaults: %+v, %v", cfg, err)
	}
	for range 2 {
		if err := paths.Ensure(); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{paths.Home, paths.Logs} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0700 {
			t.Fatalf("directory permissions: %v", info.Mode())
		}
	}
}

func TestTrustedPeerConfiguration(t *testing.T) {
	nodeID := "node_019a84fc-1b72-7000-8000-000000000001"
	for _, testCase := range []struct {
		name, address      string
		development, valid bool
	}{
		{"tailscale", "100.64.0.2", false, true},
		{"public", "8.8.8.8", false, false},
		{"hostname", "localhost", true, false},
		{"production loopback", "127.0.0.1", false, false},
		{"development loopback", "127.0.0.1", true, true},
		{"development tailscale", "100.64.0.2", true, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := Defaults()
			cfg.Network.Development = testCase.development
			if testCase.development {
				cfg.Network.BindAddress = "127.0.0.1"
			}
			cfg.TrustedPeers = []TrustedPeer{{NodeID: nodeID, Address: testCase.address, Port: 47832}}
			if err := cfg.Validate(); (err == nil) != testCase.valid {
				t.Fatalf("validation: %v", err)
			}
		})
	}
	cfg := Defaults()
	cfg.TrustedPeers = []TrustedPeer{{NodeID: nodeID, Address: "100.64.0.2", Port: 47832}, {NodeID: nodeID, Address: "100.64.0.3", Port: 47832}}
	if cfg.Validate() == nil {
		t.Fatal("duplicate trust identity accepted")
	}
	cfg = Defaults()
	cfg.Messages.RetryIntervalSeconds = 299
	if cfg.Validate() == nil {
		t.Fatal("fast long-term retries accepted")
	}
}
