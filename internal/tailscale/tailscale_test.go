package tailscale

import (
	"context"
	"errors"
	"testing"
)

func TestStatus(t *testing.T) {
	for _, testCase := range []struct {
		name, data                                    string
		missing, failure, connected, running, invalid bool
	}{
		{name: "missing", missing: true, invalid: true},
		{name: "daemon stopped", failure: true, invalid: true},
		{name: "malformed", data: `{`, invalid: true},
		{name: "empty", data: `{}`, invalid: true},
		{name: "logged out", data: `{"BackendState":"NeedsLogin"}`, running: true},
		{name: "offline", data: `{"BackendState":"Running","Self":{"Online":false},"TailscaleIPs":["100.64.0.1"]}`, running: true},
		{name: "connected", data: `{"BackendState":"Running","Self":{"ID":"self","Online":true},"TailscaleIPs":["fd7a:115c:a1e0::1","100.64.0.1"],"Peer":{"one":{"ID":"one","HostName":"peer","TailscaleIPs":["100.64.0.2"]},"self":{"ID":"self","TailscaleIPs":["100.64.0.1"]},"lan":{"ID":"lan","TailscaleIPs":["192.168.1.1"]}}}`, running: true, connected: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			client := CLI{LookPath: func(string) (string, error) {
				if testCase.missing {
					return "", errors.New("missing")
				}
				return "tailscale", nil
			}, Run: func(ctx context.Context, path string, args ...string) ([]byte, error) {
				if _, ok := ctx.Deadline(); !ok {
					t.Error("missing timeout")
				}
				if path != "tailscale" || len(args) != 2 || args[0] != "status" || args[1] != "--json" {
					t.Error("unexpected command")
				}
				if testCase.failure {
					return nil, errors.New("stopped")
				}
				return []byte(testCase.data), nil
			}}
			status, err := client.Status(context.Background())
			if (err != nil) != testCase.invalid || status.Installed == testCase.missing || status.Connected != testCase.connected || status.Running != testCase.running {
				t.Fatalf("%+v %v", status, err)
			}
			if testCase.connected && (status.IP != "100.64.0.1" || len(status.Peers) != 1) {
				t.Fatalf("%+v", status)
			}
		})
	}
}
