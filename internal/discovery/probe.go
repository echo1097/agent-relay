package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"agent-relay/internal/protocol"
)

var ErrIncompatible = errors.New("incompatible protocol version")
var ErrMalformed = errors.New("malformed hello response")

type Prober interface {
	Hello(context.Context, string, int) (protocol.Hello, error)
}

type HTTPProber struct{ Client *http.Client }

func NewProber() *HTTPProber {
	return &HTTPProber{Client: &http.Client{
		Timeout:       3 * time.Second,
		Transport:     &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext, DisableKeepAlives: true, MaxResponseHeaderBytes: 16384},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (prober *HTTPProber) Hello(ctx context.Context, ip string, port int) (protocol.Hello, error) {
	var hello protocol.Hello
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(ip, strconv.Itoa(port))+"/v1/hello", nil)
	if err != nil {
		return hello, err
	}
	request.Header.Set(protocol.VersionHeader, strconv.Itoa(protocol.Version))
	response, err := prober.Client.Do(request)
	if err != nil {
		return hello, errors.New("peer unreachable or probe timed out")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(data) > 65536 {
		return hello, ErrMalformed
	}
	if response.StatusCode != http.StatusOK {
		var errorResponse protocol.Error
		if json.Unmarshal(data, &errorResponse) == nil && errorResponse.Error.Code == protocol.UnsupportedProtocol {
			return hello, ErrIncompatible
		}
		return hello, fmt.Errorf("hello returned HTTP %d", response.StatusCode)
	}
	if json.Unmarshal(data, &hello) != nil || hello.Protocol != protocol.Name || hello.ProtocolVersion < 1 {
		return protocol.Hello{}, ErrMalformed
	}
	if hello.ProtocolVersion != protocol.Version {
		return hello, ErrIncompatible
	}
	versions := response.Header.Values(protocol.VersionHeader)
	if len(versions) > 1 || (len(versions) == 1 && versions[0] != strconv.Itoa(hello.ProtocolVersion)) {
		return hello, ErrMalformed
	}
	if hello.Validate() != nil {
		return protocol.Hello{}, ErrMalformed
	}
	return hello, nil
}
