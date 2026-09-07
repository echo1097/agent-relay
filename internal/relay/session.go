package relay

import (
	"context"
	"errors"
	"sync"
	"time"

	"agent-relay/internal/agents"
	"agent-relay/internal/messaging"
	"agent-relay/internal/transport"
)

type StatusUpdate struct {
	Status     agents.Status `json:"status,omitempty"`
	Task       *string       `json:"task,omitempty"`
	Project    *string       `json:"project,omitempty"`
	Repository *string       `json:"repository,omitempty"`
	Branch     *string       `json:"branch,omitempty"`
	Files      *[]string     `json:"files,omitempty"`
}

type Session struct {
	Directory    *Directory
	Delivery     *transport.Service
	Registration agents.Registration
	mutex        sync.Mutex
	agentID      string
	offline      bool
}

func (session *Session) Start(ctx context.Context, clientName string) (string, error) {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if session.agentID != "" {
		return session.agentID, nil
	}
	registration := session.Registration
	if registration.DisplayName == "" {
		registration.DisplayName = clientName
	}
	if registration.Provider == "" {
		registration.Provider = clientName
	}
	agent, err := session.Directory.Registry.Register(ctx, registration)
	if err != nil {
		return "", err
	}
	session.agentID = agent.ID
	return agent.ID, nil
}

func (session *Session) ID() string {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	return session.agentID
}

func (session *Session) Heartbeat(ctx context.Context) error {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if session.agentID == "" || session.offline {
		return nil
	}
	_, err := session.Directory.Registry.Heartbeat(ctx, session.agentID)
	return err
}

func (session *Session) Close(ctx context.Context) error {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if session.agentID == "" {
		return nil
	}
	session.offline = true
	return session.Directory.Registry.Disconnect(ctx, session.agentID)
}

func (session *Session) Update(ctx context.Context, update StatusUpdate) (agents.Agent, error) {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	agent, err := session.Directory.Registry.Get(ctx, session.agentID)
	if err != nil {
		return agent, err
	}
	if update.Status != "" && !update.Status.Valid() {
		return agent, errors.New("status must be online, busy, idle, or offline")
	}
	metadata := agent.Metadata
	if update.Task != nil {
		metadata.Task = *update.Task
	}
	if update.Project != nil {
		metadata.Project = *update.Project
	}
	if update.Repository != nil {
		metadata.Repository = *update.Repository
	}
	if update.Branch != nil {
		metadata.Branch = *update.Branch
	}
	if update.Files != nil {
		metadata.Files = *update.Files
	}
	status := agent.Status
	if update.Status != "" {
		status = update.Status
	}
	agent, err = session.Directory.Registry.Update(ctx, session.agentID, status, metadata)
	if err == nil {
		session.offline = status == agents.Offline
	}
	return agent, err
}

func (session *Session) Send(ctx context.Context, target, conversationID, text string, kind messaging.Type) (messaging.Message, error) {
	var peerID string
	var err error
	if conversationID != "" {
		conversation, accessErr := session.Conversation(ctx, conversationID)
		if accessErr != nil {
			return messaging.Message{}, accessErr
		}
		if conversation.RemoteAgentID != target {
			return messaging.Message{}, messaging.ErrInvalid
		}
		peerID, err = session.Delivery.Store.ConversationPeer(ctx, conversationID)
	} else {
		peerID, err = session.Directory.Route(ctx, target)
	}
	if err != nil {
		return messaging.Message{}, err
	}
	return session.Delivery.Queue(ctx, messaging.Message{SenderAgentID: session.ID(), RecipientAgentID: target, ConversationID: conversationID, Text: text, Type: kind}, peerID)
}

func (session *Session) Conversation(ctx context.Context, conversationID string) (messaging.Conversation, error) {
	conversation, err := session.Delivery.Store.GetConversation(ctx, conversationID)
	if err != nil {
		return conversation, err
	}
	if conversation.LocalAgentID != session.ID() {
		return messaging.Conversation{}, messaging.ErrNotFound
	}
	return conversation, nil
}

func (session *Session) Inbox(ctx context.Context, includeRead, markRead bool) ([]messaging.Message, error) {
	if err := session.Delivery.Store.ExpireDeliveries(ctx, time.Now().UTC()); err != nil {
		return nil, err
	}
	messages, err := session.Delivery.Store.ListInbox(ctx, session.ID(), includeRead)
	if err != nil {
		return nil, err
	}
	if markRead {
		for index, message := range messages {
			messages[index], err = session.Delivery.Store.MarkMessageRead(ctx, session.ID(), message.ID, time.Now().UTC())
			if err != nil {
				return nil, err
			}
		}
	}
	return messages, nil
}
