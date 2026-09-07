package discovery

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
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
