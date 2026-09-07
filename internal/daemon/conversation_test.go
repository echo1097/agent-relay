package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"agent-relay/internal/agents"
	"agent-relay/internal/discovery"
	"agent-relay/internal/messaging"
	"agent-relay/internal/protocol"
	"agent-relay/internal/storage"
	"agent-relay/internal/tailscale"
)

type conversationTailnet struct {
	localIP string
	peerIP  string
}

func (client conversationTailnet) Status(context.Context) (tailscale.Status, error) {
	return tailscale.Status{Installed: true, Running: true, Connected: true, IP: client.localIP, Peers: []tailscale.Peer{{ID: "test-peer", IP: client.peerIP}}}, nil
}

type conversationProber struct {
	address string
}

func (prober conversationProber) Hello(ctx context.Context, ip string, port int) (protocol.Hello, error) {
	host, portText, err := net.SplitHostPort(prober.address)
	if err != nil {
		return protocol.Hello{}, err
	}
	localPort, err := strconv.Atoi(portText)
	if err != nil {
		return protocol.Hello{}, err
	}
	return discovery.NewProber().Hello(ctx, host, localPort)
}

func TestAgentRelayEndToEndConversation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	nodeA := makeDeliveryNode(t, "127.0.0.1:0")
	nodeB := makeDeliveryNode(t, "127.0.0.1:0")
	nodeB.start(t)
	trustNode(t, nodeA, nodeB)
	nodeA.start(t)
	nodeB.stop(t)
	trustNode(t, nodeB, nodeA)
	nodeB.start(t)

	for _, pair := range [][2]*deliveryNode{{nodeA, nodeB}, {nodeB, nodeA}} {
		source, target := pair[0], pair[1]
		manager := discovery.Manager{Client: conversationTailnet{localIP: "100.64.0.1", peerIP: "100.64.0.2"}, Prober: conversationProber{address: target.address}, LocalID: source.node.ID, Path: filepath.Join(source.home, "peers.json")}
		snapshot, err := manager.Refresh(ctx)
		if err != nil || len(snapshot.Peers) != 1 || snapshot.Peers[0].Node.ID != target.node.ID || snapshot.Peers[0].State != "online" {
			t.Fatalf("peer discovery: %+v %v", snapshot, err)
		}
	}

	response, err := nodeA.service.Client.Get("http://" + nodeB.address + "/v1/agents")
	if err != nil {
		t.Fatal(err)
	}
	var agentList protocol.AgentList
	decodeErr := json.NewDecoder(response.Body).Decode(&agentList)
	response.Body.Close()
	if response.StatusCode != 200 || decodeErr != nil || agentList.Validate() != nil || len(agentList.Agents) != 1 || agentList.Agents[0].ID != nodeB.agent.ID {
		t.Fatalf("Agent A lists Agent B: %+v %v", agentList, decodeErr)
	}

	conversationID := ""
	messageIDs := []string{}
	texts := []string{"Did you change refresh token validation?", "Yes, I changed the validation logic.", "Was session_id added?", "Yes."}
	for turn := range 2 {
		question, err := nodeA.service.Queue(ctx, messaging.Message{SenderAgentID: nodeA.agent.ID, RecipientAgentID: nodeB.agent.ID, ConversationID: conversationID, Type: messaging.Question, Text: texts[turn*2]}, nodeB.node.ID)
		if err != nil {
			t.Fatal(err)
		}
		conversationID = question.ConversationID
		waitMessage(t, nodeB.store, question.ID, messaging.Delivered)
		inbox, err := nodeB.store.ListInbox(ctx, nodeB.agent.ID, true)
		if err != nil || len(inbox) != turn+1 || inbox[turn].ID != question.ID || inbox[turn].ConversationID != conversationID {
			t.Fatalf("B receives question: %+v %v", inbox, err)
		}
		answer, err := nodeB.service.Respond(ctx, nodeB.agent.ID, question.ID, texts[turn*2+1])
		if err != nil {
			t.Fatal(err)
		}
		waitMessage(t, nodeA.store, answer.ID, messaging.Delivered)
		waitMessage(t, nodeB.store, answer.ID, messaging.Delivered)
		inbox, err = nodeA.store.ListInbox(ctx, nodeA.agent.ID, true)
		if err != nil || len(inbox) != turn+1 || inbox[turn].ID != answer.ID || inbox[turn].ReplyTo != question.ID || inbox[turn].ConversationID != conversationID {
			t.Fatalf("A receives linked answer: %+v %v", inbox, err)
		}
		for _, node := range []*deliveryNode{nodeA, nodeB} {
			answered := waitMessage(t, node.store, question.ID, messaging.Answered)
			if answered.AnsweredAt == nil {
				t.Fatal("missing answered timestamp")
			}
		}
		if _, err := nodeB.service.Respond(ctx, nodeB.agent.ID, question.ID, "second answer"); !errors.Is(err, messaging.ErrTransition) {
			t.Fatalf("duplicate answer: %v", err)
		}
		for range 2 {
			if retry, reason := nodeB.service.Send(ctx, answer, nodeA.node.ID); retry || reason != "" {
				t.Fatalf("response retry: %v %s", retry, reason)
			}
		}
		messageIDs = append(messageIDs, question.ID, answer.ID)
	}

	for _, node := range []*deliveryNode{nodeA, nodeB} {
		node.stop(t)
		if err := node.store.Close(); err != nil {
			t.Fatal(err)
		}
		reopened, err := storage.Open(ctx, filepath.Join(node.home, "relay.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { reopened.Close() })
		history, err := reopened.ConversationHistory(ctx, conversationID)
		if err != nil || len(history) != 4 {
			t.Fatalf("persisted history: %+v %v", history, err)
		}
		for index, message := range history {
			if message.ID != messageIDs[index] || message.Text != texts[index] || message.ConversationID != conversationID {
				t.Fatalf("ordered history at %d: %+v", index, message)
			}
			if index%2 == 0 && (message.Status != messaging.Answered || message.AnsweredAt == nil) {
				t.Fatalf("persisted answered state: %+v", message)
			}
		}
		conversation, err := reopened.GetConversation(ctx, conversationID)
		if err != nil || !conversation.UpdatedAt.Equal(history[3].CreatedAt) || !conversation.UpdatedAt.After(conversation.CreatedAt) {
			t.Fatalf("conversation timestamps: %+v %v", conversation, err)
		}
	}
}

func TestResponseValidationAndConversationMembership(t *testing.T) {
	ctx := context.Background()
	nodeA := makeDeliveryNode(t, "127.0.0.1:0")
	nodeB := makeDeliveryNode(t, "127.0.0.1:0")
	trustNode(t, nodeA, nodeB)
	trustNode(t, nodeB, nodeA)
	question, err := nodeA.service.Queue(ctx, messaging.Message{SenderAgentID: nodeA.agent.ID, RecipientAgentID: nodeB.agent.ID, Type: messaging.Question, Text: "question"}, nodeB.node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := nodeB.store.ReceiveRemote(ctx, question, nodeA.node.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	for _, input := range [][2]string{{nodeA.agent.ID, question.ID}, {nodeB.agent.ID, "missing"}} {
		if _, err := nodeB.service.Respond(ctx, input[0], input[1], "invalid"); err == nil {
			t.Fatalf("accepted invalid responder or question: %v", input)
		}
	}
	missingConversation, err := messaging.NewID("conv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := nodeA.service.Queue(ctx, messaging.Message{SenderAgentID: nodeA.agent.ID, RecipientAgentID: nodeB.agent.ID, Type: messaging.Question, Text: "bad follow-up", ConversationID: missingConversation}, nodeB.node.ID); !errors.Is(err, messaging.ErrNotFound) {
		t.Fatalf("unknown follow-up conversation: %v", err)
	}
	outsider, err := nodeA.registry.Register(ctx, agents.Registration{DisplayName: "outsider"})
	if err != nil {
		t.Fatal(err)
	}
	for _, incoming := range []bool{false, true} {
		message := question
		message.ID, err = messaging.NewID("msg")
		if err != nil {
			t.Fatal(err)
		}
		message.SenderAgentID = outsider.ID
		if incoming {
			_, _, err = nodeB.store.ReceiveRemote(ctx, message, nodeA.node.ID, time.Now().UTC())
		} else {
			_, err = nodeA.service.Queue(ctx, message, nodeB.node.ID)
		}
		if !errors.Is(err, messaging.ErrConflict) {
			t.Fatalf("outsider appended to conversation: %v", err)
		}
	}
	answer, err := nodeB.service.Respond(ctx, nodeB.agent.ID, question.ID, "yes")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := nodeB.service.Respond(ctx, nodeB.agent.ID, answer.ID, "response to response"); !errors.Is(err, messaging.ErrInvalid) {
		t.Fatalf("response to non-question: %v", err)
	}
	server, err := newHTTPServer(nodeA.registry, HTTPOptions{Node: protocol.PublicNode(nodeA.node.ID, "test"), Version: "test", Delivery: nodeA.service}, nodeA.service.Logger)
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name   string
		change func(*protocol.Message)
		status int
	}{
		{name: "missing original", change: func(message *protocol.Message) { message.ReplyTo, _ = messaging.NewID("msg") }, status: 404},
		{name: "wrong conversation", change: func(message *protocol.Message) { message.ConversationID = missingConversation }, status: 400},
		{name: "wrong recipient", change: func(message *protocol.Message) { message.RecipientAgentID = outsider.ID }, status: 409},
		{name: "missing link", change: func(message *protocol.Message) { message.ReplyTo = "" }, status: 400},
		{name: "question on response endpoint", change: func(message *protocol.Message) { message.Type = messaging.MessageType; message.ReplyTo = "" }, status: 400},
		{name: "valid response", status: 200},
		{name: "exact retry", status: 200},
		{name: "different second response", change: func(message *protocol.Message) { message.ID, _ = messaging.NewID("msg") }, status: 409},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			wire := protocol.WireMessage(answer)
			if testCase.change != nil {
				testCase.change(&wire)
			}
			data, err := json.Marshal(wire)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(data))
			request.RemoteAddr = "127.0.0.1:12345"
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set(protocol.NodeHeader, nodeB.node.ID)
			writer := httptest.NewRecorder()
			server.Handler.ServeHTTP(writer, request)
			if writer.Code != testCase.status {
				t.Fatalf("status %d: %s", writer.Code, writer.Body)
			}
		})
	}
	if _, err := nodeA.store.GetConversation(ctx, missingConversation); !errors.Is(err, messaging.ErrNotFound) {
		t.Fatalf("invalid response left an orphan conversation: %v", err)
	}
	for _, node := range []*deliveryNode{nodeA, nodeB} {
		history, err := node.store.ConversationHistory(ctx, question.ConversationID)
		if err != nil || len(history) != 2 {
			t.Fatalf("invalid response changed history: %+v %v", history, err)
		}
	}
}

func TestResponseLostAcknowledgmentSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	nodeA := makeDeliveryNode(t, "127.0.0.1:0")
	nodeB := makeDeliveryNode(t, "127.0.0.1:0")
	nodeB.start(t)
	trustNode(t, nodeA, nodeB)
	nodeA.start(t)
	nodeB.stop(t)
	trustNode(t, nodeB, nodeA)
	baseTransport := nodeB.service.Client.Transport
	nodeB.service.Client.Transport = &lostAckTransport{base: baseTransport}
	t.Cleanup(func() { baseTransport.(*http.Transport).CloseIdleConnections() })
	nodeB.start(t)

	question, err := nodeA.service.Queue(ctx, messaging.Message{SenderAgentID: nodeA.agent.ID, RecipientAgentID: nodeB.agent.ID, Type: messaging.Question, Text: "Will the answer survive a lost acknowledgment?"}, nodeB.node.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitMessage(t, nodeB.store, question.ID, messaging.Delivered)
	answer, err := nodeB.service.Respond(ctx, nodeB.agent.ID, question.ID, "Yes.")
	if err != nil {
		t.Fatal(err)
	}
	waitMessage(t, nodeB.store, answer.ID, messaging.PendingDelivery)
	received := waitMessage(t, nodeA.store, answer.ID, messaging.Delivered)
	answered := waitMessage(t, nodeA.store, question.ID, messaging.Answered)
	nodeB.stop(t)
	if err := nodeB.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := storage.Open(ctx, filepath.Join(nodeB.home, "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	nodeB.store = reopened
	nodeB.service.Store = reopened
	nodeB.registry, err = agents.New(reopened, nodeB.node.ID, agents.Options{OfflineAfter: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	nodeB.start(t)
	waitMessage(t, reopened, answer.ID, messaging.Delivered)
	inbox, err := nodeA.store.ListInbox(ctx, nodeA.agent.ID, true)
	if err != nil || len(inbox) != 1 || inbox[0].ReceivedAt == nil || !inbox[0].ReceivedAt.Equal(*received.ReceivedAt) {
		t.Fatalf("retried response changed inbox: %+v %v", inbox, err)
	}
	afterRetry := waitMessage(t, nodeA.store, question.ID, messaging.Answered)
	if answered.AnsweredAt == nil || afterRetry.AnsweredAt == nil || !afterRetry.AnsweredAt.Equal(*answered.AnsweredAt) {
		t.Fatal("retry changed answered timestamp")
	}
}
