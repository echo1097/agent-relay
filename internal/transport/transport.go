package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"agent-relay/internal/config"
	"agent-relay/internal/discovery"
	"agent-relay/internal/messaging"
	"agent-relay/internal/protocol"
	"agent-relay/internal/storage"
	"agent-relay/internal/tailscale"
)

type Service struct {
	Store         *storage.Store
	NodeID        string
	Tailscale     tailscale.Client
	Development   bool
	Prober        discovery.Prober
	Client        *http.Client
	RetryInterval time.Duration
	Lifetime      time.Duration
	Logger        *slog.Logger
}

func New(store *storage.Store, nodeID, localIP string, cfg config.Config, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	dialer := &net.Dialer{Timeout: 3 * time.Second}
	if localIP != "" {
		dialer.LocalAddr = &net.TCPAddr{IP: net.ParseIP(localIP)}
	}
	client := &http.Client{
		Timeout:       5 * time.Second,
		Transport:     &http.Transport{DialContext: dialer.DialContext, MaxResponseHeaderBytes: 16 * 1024, ResponseHeaderTimeout: 5 * time.Second, IdleConnTimeout: 30 * time.Second},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &Service{Store: store, NodeID: nodeID, Tailscale: tailscale.New(), Development: cfg.Network.Development, Prober: discovery.NewProber(), Client: client, RetryInterval: time.Duration(cfg.Messages.RetryIntervalSeconds) * time.Second, Lifetime: time.Duration(cfg.Messages.RequestExpirationHours) * time.Hour, Logger: logger}
}

func (service *Service) Queue(ctx context.Context, message messaging.Message, peerID string) (messaging.Message, error) {
	if _, err := service.trustedPeer(ctx, peerID); err != nil {
		return messaging.Message{}, err
	}
	now := time.Now().UTC()
	var err error
	if message.ID == "" {
		message.ID, err = messaging.NewID("msg")
		if err != nil {
			return messaging.Message{}, err
		}
	}
	if message.ConversationID != "" {
		if _, err := service.Store.GetConversation(ctx, message.ConversationID); err != nil {
			return messaging.Message{}, err
		}
	}
	if message.ConversationID == "" {
		message.ConversationID, err = messaging.NewID("conv")
		if err != nil {
			return messaging.Message{}, err
		}
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = now
	}
	deadline := now.Add(service.Lifetime)
	if message.Type == messaging.Question {
		if message.ExpiresAt == nil {
			expiresAt := message.CreatedAt.Add(service.Lifetime)
			message.ExpiresAt = &expiresAt
		}
		deadline = *message.ExpiresAt
	}
	if err := protocol.WireMessage(message).Validate(); err != nil {
		return messaging.Message{}, err
	}
	saved, inserted, err := service.Store.QueueAuthorized(ctx, message, peerID, now, deadline)
	if err == nil && inserted {
		service.Logger.Info("message queued", "message_id", saved.ID, "peer_id", peerID, "type", saved.Type)
	}
	return saved, err
}

func (service *Service) Respond(ctx context.Context, agentID, messageID, text string) (messaging.Message, error) {
	question, err := service.Store.GetMessage(ctx, messageID)
	if err != nil {
		return messaging.Message{}, err
	}
	if question.Type != messaging.Question || question.RecipientAgentID != agentID || question.ReceivedAt == nil {
		return messaging.Message{}, messaging.ErrInvalid
	}
	peerID, err := service.Store.ConversationPeer(ctx, question.ConversationID)
	if err != nil {
		return messaging.Message{}, err
	}
	return service.Queue(ctx, messaging.Message{
		SenderAgentID:    agentID,
		RecipientAgentID: question.SenderAgentID,
		ConversationID:   question.ConversationID,
		Type:             messaging.Response,
		ReplyTo:          question.ID,
		Text:             text,
	}, peerID)
}

func RetryDelay(attempts int, interval time.Duration) time.Duration {
	delays := []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second, time.Minute, 5 * time.Minute}
	if attempts < 0 {
		attempts = 0
	}
	if attempts < len(delays) {
		return delays[attempts]
	}
	return interval
}

func (service *Service) Send(ctx context.Context, message messaging.Message, peerID string) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	peer, err := service.trustedPeer(ctx, peerID)
	if err != nil {
		var authorization *AuthorizationError
		if errors.As(err, &authorization) {
			return false, authorization.Error()
		}
		return true, "LOCAL_STORAGE_UNAVAILABLE"
	}
	if !service.Development {
		address, err := service.peerAddress(ctx, peer)
		if err != nil {
			return true, "PEER_UNREACHABLE: unable to verify Tailscale identity"
		}
		peer.Address = address
		hello, err := service.Prober.Hello(ctx, address, peer.Port)
		if err != nil {
			if errors.Is(err, discovery.ErrIncompatible) {
				return false, protocol.UnsupportedProtocol + ": upgrade both Relay nodes to compatible versions"
			}
			return true, "PEER_UNREACHABLE: unable to verify Relay identity"
		}
		if hello.Node.ID != peer.NodeID {
			return false, protocol.NodeNotTrusted + ": destination Relay identity changed; verify the peer before trusting it"
		}
	}
	wire := protocol.WireMessage(message)
	if err := wire.Validate(); err != nil {
		return false, protocol.InvalidRequest
	}
	data, err := json.Marshal(wire)
	if err != nil {
		return false, protocol.InvalidRequest
	}
	path := "/v1/messages"
	if message.Type == messaging.Response {
		path = "/v1/responses"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+net.JoinHostPort(peer.Address, strconv.Itoa(peer.Port))+path, bytes.NewReader(data))
	if err != nil {
		return false, protocol.InvalidRequest
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(protocol.VersionHeader, "1")
	request.Header.Set(protocol.NodeHeader, service.NodeID)
	response, err := service.Client.Do(request)
	if err != nil {
		return true, "PEER_UNREACHABLE"
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		if response.StatusCode == 408 || response.StatusCode == 429 || response.StatusCode >= 500 {
			return true, "PEER_UNAVAILABLE"
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, 64*1024+1))
		var rejection protocol.Error
		if err == nil && len(data) <= 64*1024 && json.Unmarshal(data, &rejection) == nil && rejection.Validate() == nil {
			switch rejection.Error.Code {
			case protocol.DoNotDisturb:
				return false, protocol.DoNotDisturb + ": the receiving computer has DND enabled; send again when available"
			case protocol.NodeNotTrusted:
				return false, protocol.NodeNotTrusted + ": ask the receiving owner to inspect trust-state and verify the sender"
			case protocol.UnsupportedProtocol:
				return false, protocol.UnsupportedProtocol + ": upgrade both Relay nodes to compatible versions"
			}
		}
		return false, "DELIVERY_REJECTED"
	}
	data, err = io.ReadAll(io.LimitReader(response.Body, 64*1024+1))
	if err != nil || len(data) > 64*1024 {
		return true, "INVALID_ACKNOWLEDGMENT"
	}
	var ack protocol.DeliveryAck
	versions := response.Header.Values(protocol.VersionHeader)
	if json.Unmarshal(data, &ack) != nil || ack.Validate() != nil || ack.MessageID != message.ID || ack.NodeID != peerID || len(versions) != 1 || versions[0] != "1" {
		return true, "INVALID_ACKNOWLEDGMENT"
	}
	return false, ""
}

func (service *Service) Tick(ctx context.Context) error {
	now := time.Now().UTC()
	if err := service.Store.ExpireDeliveries(ctx, now); err != nil {
		return err
	}
	due, err := service.Store.DueDeliveries(ctx, now)
	if err != nil {
		return err
	}
	for _, item := range due {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		message, err := service.Store.GetMessage(ctx, item.MessageID)
		if err != nil {
			return err
		}
		if message.Status != messaging.Sending {
			if _, err := service.Store.UpdateMessageStatus(ctx, message.ID, messaging.Sending, time.Now().UTC()); err != nil {
				if errors.Is(err, messaging.ErrTransition) {
					continue
				}
				return err
			}
		}
		attemptCtx, cancel := context.WithDeadline(ctx, item.Deadline)
		retry, reason := service.Send(attemptCtx, message, item.NodeID)
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		status := messaging.Delivered
		if reason != "" {
			status = messaging.Failed
			if retry {
				status = messaging.PendingDelivery
			}
		}
		now = time.Now().UTC()
		if err := service.Store.FinishDelivery(ctx, message.ID, status, now.Add(RetryDelay(item.Attempts, service.RetryInterval)), now, reason); err != nil {
			return err
		}
		event := "message delivered"
		if message.Type == messaging.Response {
			event = "response delivered"
		}
		if reason != "" {
			event = "delivery failed"
		}
		service.Logger.Info(event, "message_id", message.ID, "peer_id", item.NodeID, "status", status, "reason", reason)
	}
	return service.Store.ExpireDeliveries(ctx, time.Now().UTC())
}

func (service *Service) Run(ctx context.Context) {
	defer service.Client.CloseIdleConnections()
	delay := 250 * time.Millisecond
	for {
		if err := service.Tick(ctx); err != nil && ctx.Err() == nil {
			service.Logger.Error("delivery queue processing failed")
			delay *= 2
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
		} else {
			delay = 250 * time.Millisecond
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
