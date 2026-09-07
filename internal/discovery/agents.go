package discovery

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"agent-relay/internal/protocol"
)

func (prober *HTTPProber) Agents(ctx context.Context, address string, port int) ([]protocol.Agent, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(address, strconv.Itoa(port))+"/v1/agents", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set(protocol.VersionHeader, strconv.Itoa(protocol.Version))
	response, err := prober.Client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 1024*1024 || response.StatusCode != http.StatusOK || response.Header.Get(protocol.VersionHeader) != "1" {
		return nil, ErrMalformed
	}
	var result protocol.AgentList
	if json.Unmarshal(data, &result) != nil || result.Validate() != nil {
		return nil, ErrMalformed
	}
	return result.Agents, nil
}
