package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"agent-relay/internal/messaging"
	"agent-relay/internal/protocol"
	"agent-relay/internal/storage"
	"agent-relay/internal/transport"
)

func (handler *httpHandler) receiveMessage(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		handler.writeError(writer, 405, protocol.MethodNotAllowed, "This endpoint accepts POST requests only.")
		return
	}
	nodeIDs := request.Header.Values(protocol.NodeHeader)
	if handler.delivery == nil || len(nodeIDs) != 1 {
		handler.writeError(writer, 403, protocol.NodeNotTrusted, "The sending node is not trusted.")
		return
	}
	if err := handler.delivery.Authorize(request.Context(), nodeIDs[0], request.RemoteAddr); err != nil {
		var authorizationError *transport.AuthorizationError
		switch {
		case errors.As(err, &authorizationError):
			handler.writeError(writer, 403, protocol.NodeNotTrusted, authorizationError.Detail)
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			handler.writeError(writer, 504, protocol.RequestTimeout, "Trust verification timed out.")
		default:
			handler.writeError(writer, 500, protocol.InternalError, "Trust state is unavailable. Check the local database.")
		}
		return
	}
	contentType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" || request.URL.RawQuery != "" {
		handler.writeError(writer, 400, protocol.InvalidRequest, "A JSON message without query parameters is required.")
		return
	}
	if err := http.NewResponseController(writer).SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		handler.writeError(writer, 500, protocol.InternalError, "The request could not be read.")
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 512*1024)
	defer request.Body.Close()
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var message protocol.Message
	if err := decoder.Decode(&message); err != nil {
		handler.writeError(writer, 400, protocol.InvalidRequest, "The message is invalid or too large.")
		return
	}
	if decoder.Decode(new(any)) != io.EOF {
		handler.writeError(writer, 400, protocol.InvalidRequest, "Specify exactly one message.")
		return
	}
	if message.ProtocolVersion != protocol.Version {
		handler.writeError(writer, 400, protocol.UnsupportedProtocol, "This node supports protocol version 1.")
		return
	}
	if err := message.Validate(); err != nil {
		handler.writeError(writer, 400, protocol.InvalidRequest, "The message is invalid.")
		return
	}
	if (request.URL.Path == "/v1/responses") != (message.Type == messaging.Response) {
		handler.writeError(writer, 400, protocol.InvalidRequest, "Use the endpoint matching the message type.")
		return
	}
	saved, inserted, err := handler.delivery.Store.ReceiveAuthorized(request.Context(), message.Local(), nodeIDs[0], time.Now().UTC())
	if err != nil {
		status, code, detail := 500, protocol.InternalError, "The message could not be stored."
		switch {
		case errors.Is(err, storage.ErrDnd):
			status, code, detail = 403, protocol.DoNotDisturb, "This computer has do not disturb enabled. Try again after its owner turns DND off."
		case errors.Is(err, storage.ErrNodeNotTrusted):
			status, code, detail = 403, protocol.NodeNotTrusted, "Trust was revoked before this message could be stored. Ask the receiving owner to inspect peer trust."
		case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
			status, code, detail = 504, protocol.RequestTimeout, "The request timed out."
		case errors.Is(err, messaging.ErrNotFound):
			status, code, detail = 404, protocol.AgentNotFound, "The recipient is not registered on this node."
			if message.Type == messaging.Response {
				code, detail = protocol.MessageNotFound, "The original message or recipient does not exist on this node."
			}
		case errors.Is(err, messaging.ErrTransition), errors.Is(err, messaging.ErrConflict):
			status, code, detail = 409, protocol.MessageConflict, "The message conflicts with an existing message or conversation."
		case errors.Is(err, messaging.ErrExpired):
			status, code, detail = 410, protocol.MessageExpired, "The message has expired."
		case errors.Is(err, messaging.ErrInvalid):
			status, code, detail = 400, protocol.InvalidRequest, "The message participants are invalid."
		}
		handler.writeError(writer, status, code, detail)
		return
	}
	if saved.ReceivedAt == nil {
		handler.writeError(writer, 409, protocol.MessageConflict, "The message ID is already in use.")
		return
	}
	if inserted {
		handler.logger.Info("message received", "message_id", saved.ID, "peer_id", nodeIDs[0], "type", saved.Type)
	}
	handler.writeJSON(writer, 200, protocol.DeliveryAck{ProtocolVersion: protocol.Version, NodeID: handler.hello.Node.ID, MessageID: saved.ID, Status: "delivered", ReceivedAt: *saved.ReceivedAt})
}
