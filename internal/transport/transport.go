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
	"agent-relay/internal/messaging"
	"agent-relay/internal/protocol"
	"agent-relay/internal/storage"
)

type Service struct {
	Store         *storage.Store
	NodeID        string
	Peers         []config.TrustedPeer
	Client        *http.Client
	RetryInterval time.Duration
	Lifetime      time.Duration
	Logger        *slog.Logger
}

func New(store *storage.Store, nodeID, localIP string, cfg config.Config, logger *slog.Logger) *Service {
	dialer := &net.Dialer{Timeout: 3 * time.Second}
	if localIP != "" {
		dialer.LocalAddr = &net.TCPAddr{IP: net.ParseIP(localIP)}
	}
	client := &http.Client{
		Timeout:       5 * time.Second,
		Transport:     &http.Transport{DialContext: dialer.DialContext, MaxResponseHeaderBytes: 16 * 1024, ResponseHeaderTimeout: 5 * time.Second, IdleConnTimeout: 30 * time.Second},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &Service{Store: store, NodeID: nodeID, Peers: append([]config.TrustedPeer(nil), cfg.TrustedPeers...), Client: client, RetryInterval: time.Duration(cfg.Messages.RetryIntervalSeconds) * time.Second, Lifetime: time.Duration(cfg.Messages.RequestExpirationHours) * time.Hour, Logger: logger}
}

func (service *Service) Peer(nodeID string) (config.TrustedPeer, bool) {
	for _, peer := range service.Peers {
		if peer.NodeID == nodeID && nodeID != service.NodeID {
			return peer, true
		}
	}
	return config.TrustedPeer{}, false
}

func (service *Service) Trusted(nodeID, remoteAddress string) bool {
	peer, ok := service.Peer(nodeID)
	host, _, err := net.SplitHostPort(remoteAddress)
	return ok && err == nil && net.ParseIP(host).Equal(net.ParseIP(peer.Address))
}

func (service *Service) Queue(ctx context.Context, message messaging.Message, peerID string) (messaging.Message, error) {
	if _, ok := service.Peer(peerID); !ok {
		return messaging.Message{}, errors.New(protocol.NodeNotTrusted)
	}
	now := time.Now().UTC()
	var err error
	if message.ID == "" {
		message.ID, err = messaging.NewID("msg")
		if err != nil {
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
	saved, _, err := service.Store.QueueMessage(ctx, message, peerID, now, deadline)
	return saved, err
}

func RetryDelay(attempts int, interval time.Duration) time.Duration {
	delays := []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second, time.Minute, 5 * time.Minute}
	if attempts < len(delays) {
		return delays[attempts]
	}
	return interval
}

func (service *Service) Send(ctx context.Context, message messaging.Message, peerID string) (bool, string) {
	peer, ok := service.Peer(peerID)
	if !ok {
		return false, protocol.NodeNotTrusted
	}
	wire := protocol.WireMessage(message)
	if err := wire.Validate(); err != nil {
		return false, protocol.InvalidRequest
	}
	data, err := json.Marshal(wire)
	if err != nil {
		return false, protocol.InvalidRequest
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+net.JoinHostPort(peer.Address, strconv.Itoa(peer.Port))+"/v1/messages", bytes.NewReader(data))
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
		return false, "DELIVERY_REJECTED"
	}
	data, err = io.ReadAll(io.LimitReader(response.Body, 64*1024+1))
	if err != nil || len(data) > 64*1024 {
		return true, "INVALID_ACKNOWLEDGMENT"
	}
	var ack protocol.DeliveryAck
	if json.Unmarshal(data, &ack) != nil || ack.Validate() != nil || ack.MessageID != message.ID || ack.NodeID != peerID || response.Header.Get(protocol.VersionHeader) != "1" {
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
		if _, err := service.Store.UpdateMessageStatus(ctx, message.ID, messaging.Sending, time.Now().UTC()); err != nil {
			if errors.Is(err, messaging.ErrTransition) {
				continue
			}
			return err
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
	}
	return service.Store.ExpireDeliveries(ctx, time.Now().UTC())
}

func (service *Service) Run(ctx context.Context) {
	defer service.Client.CloseIdleConnections()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := service.Tick(ctx); err != nil && ctx.Err() == nil {
			service.Logger.Error("delivery queue processing failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
