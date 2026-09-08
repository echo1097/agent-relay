package discovery

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAgentProbeValidation(t *testing.T) {
	for _, testCase := range []struct {
		name, body, header string
		valid              bool
	}{
		{"empty list", `{"protocol_version":1,"agents":[]}`, "1", true},
		{"missing header", `{"protocol_version":1,"agents":[]}`, "", false},
		{"invalid agent", `{"protocol_version":1,"agents":[{"id":"bad"}]}`, "1", false},
		{"missing list", `{"protocol_version":1}`, "1", false},
		{"oversized", strings.Repeat(" ", 1024*1024+1), "1", false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/v1/agents" || request.Header.Get("X-Agent-Relay-Protocol-Version") != "1" {
					t.Error("invalid probe request")
				}
				writer.Header().Set("X-Agent-Relay-Protocol-Version", testCase.header)
				fmt.Fprint(writer, testCase.body)
			}))
			defer server.Close()
			address, portText, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
			if err != nil {
				t.Fatal(err)
			}
			port, err := strconv.Atoi(portText)
			if err != nil {
				t.Fatal(err)
			}
			_, err = NewProber().Agents(context.Background(), address, port)
			if (err == nil) != testCase.valid {
				t.Fatalf("valid=%v, error=%v", testCase.valid, err)
			}
		})
	}
}

func TestArchivedAgentProbeFallsBackForOlderPeers(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.URL.RawQuery != "" {
			if request.URL.Query().Get("include_archived") != "true" {
				t.Error("missing archive query")
			}
			http.Error(writer, "query parameters are not supported", http.StatusBadRequest)
			return
		}
		writer.Header().Set("X-Agent-Relay-Protocol-Version", "1")
		fmt.Fprint(writer, `{"protocol_version":1,"agents":[{"id":"agent_019a84fc-1b72-7000-8000-000000000002","display_name":"older peer","status":"offline"}]}`)
	}))
	defer server.Close()
	address, portText, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	prober := NewProber()
	defer prober.Client.CloseIdleConnections()
	listing, err := prober.AgentsIncludingArchived(context.Background(), address, port)
	if err != nil || len(listing) != 1 || requests.Load() != 2 {
		t.Fatalf("older peer fallback: %+v %v, requests=%d", listing, err, requests.Load())
	}
	if listing[0].Archived || listing[0].LastSeenAt != nil {
		t.Fatalf("invented metadata for older peer: %+v", listing[0])
	}
}
