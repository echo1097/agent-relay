package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"agent-relay/internal/agents"
	"agent-relay/internal/protocol"
	"agent-relay/internal/transport"
)

type HTTPOptions struct {
	Delivery   *transport.Service
	Background func(context.Context)
	Address    string
	Node       protocol.Node
	Version    string
	Ready      func(net.Addr)
}

type agentLister interface {
	List(context.Context) ([]agents.Agent, error)
}

type httpHandler struct {
	delivery *transport.Service
	registry agentLister
	hello    protocol.Hello
	logger   *slog.Logger
}

type responseObject interface{ Validate() error }

func newHTTPServer(registry agentLister, options HTTPOptions, logger *slog.Logger) (*http.Server, error) {
	hello := protocol.Hello{Protocol: protocol.Name, ProtocolVersion: protocol.Version, Node: options.Node, Version: options.Version}
	if err := hello.Validate(); err != nil {
		return nil, err
	}
	handler := &httpHandler{delivery: options.Delivery, registry: registry, hello: hello, logger: logger}
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 * 1024,
		ErrorLog:          slog.NewLogLogger(slog.NewTextHandler(io.Discard, nil), slog.LevelError),
	}, nil
}

func (handler *httpHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set(protocol.VersionHeader, strconv.Itoa(protocol.Version))
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	defer func() {
		if recover() != nil {
			handler.logger.Error("HTTP handler failed")
			handler.writeError(writer, http.StatusInternalServerError, protocol.InternalError, "The request could not be completed.")
		}
	}()
	ctx, cancel := context.WithTimeout(request.Context(), 5*time.Second)
	defer cancel()
	versions := request.Header.Values(protocol.VersionHeader)
	if len(versions) > 1 {
		handler.writeError(writer, http.StatusBadRequest, protocol.InvalidRequest, "Specify one protocol version.")
		return
	}
	if len(versions) == 1 {
		version, err := strconv.Atoi(versions[0])
		if err != nil || version < 1 || strconv.Itoa(version) != versions[0] {
			handler.writeError(writer, http.StatusBadRequest, protocol.InvalidRequest, "The protocol version must be a positive integer.")
			return
		}
		if version != protocol.Version {
			handler.writeError(writer, http.StatusBadRequest, protocol.UnsupportedProtocol, "This node supports protocol version 1.")
			return
		}
	}
	path := request.URL.Path
	if path == "/v1/messages" {
		handler.receiveMessage(writer, request.WithContext(ctx))
		return
	}
	if strings.HasPrefix(path, "/v") && !strings.HasPrefix(path, "/v1/") {
		handler.writeError(writer, http.StatusBadRequest, protocol.UnsupportedProtocol, "This node supports protocol version 1.")
		return
	}
	if path != "/v1/health" && path != "/v1/hello" && path != "/v1/agents" {
		handler.writeError(writer, http.StatusNotFound, protocol.NotFound, "The requested endpoint does not exist.")
		return
	}
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		handler.writeError(writer, http.StatusMethodNotAllowed, protocol.MethodNotAllowed, "This endpoint accepts GET requests only.")
		return
	}
	if request.ContentLength != 0 || len(request.TransferEncoding) != 0 || request.URL.RawQuery != "" {
		handler.writeError(writer, http.StatusBadRequest, protocol.InvalidRequest, "Request bodies and query parameters are not supported.")
		return
	}
	switch path {
	case "/v1/health":
		handler.writeJSON(writer, http.StatusOK, protocol.Health{ProtocolVersion: protocol.Version, Status: "ok"})
	case "/v1/hello":
		handler.writeJSON(writer, http.StatusOK, handler.hello)
	case "/v1/agents":
		localAgents, err := handler.registry.List(ctx)
		if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
			handler.writeError(writer, http.StatusGatewayTimeout, protocol.RequestTimeout, "The request timed out.")
			return
		}
		if err != nil {
			handler.logger.Error("HTTP agent listing failed")
			handler.writeError(writer, http.StatusInternalServerError, protocol.InternalError, "Local agents are temporarily unavailable.")
			return
		}
		response := protocol.AgentList{ProtocolVersion: protocol.Version, Agents: []protocol.Agent{}}
		for _, agent := range localAgents {
			if agent.NodeID == handler.hello.Node.ID {
				response.Agents = append(response.Agents, protocol.PublicAgent(agent))
			}
		}
		handler.writeJSON(writer, http.StatusOK, response)
	}
}

func (handler *httpHandler) writeJSON(writer http.ResponseWriter, status int, response responseObject) {
	if err := response.Validate(); err != nil {
		handler.logger.Error("HTTP response validation failed")
		response = protocol.Error{ProtocolVersion: protocol.Version, Error: protocol.ErrorDetail{Code: protocol.InternalError, Message: "The response could not be produced."}}
		status = http.StatusInternalServerError
	}
	data, err := json.Marshal(response)
	if err != nil {
		handler.logger.Error("HTTP response encoding failed")
		return
	}
	writer.WriteHeader(status)
	if _, err := writer.Write(append(data, '\n')); err != nil {
		handler.logger.Debug("HTTP response write failed")
	}
}

func (handler *httpHandler) writeError(writer http.ResponseWriter, status int, code, message string) {
	handler.writeJSON(writer, status, protocol.Error{ProtocolVersion: protocol.Version, Error: protocol.ErrorDetail{Code: code, Message: message}})
}
