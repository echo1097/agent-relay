package daemon

import (
	"agent-relay/internal/protocol"
	"net/http"
	"sync"
)

type requestTracker struct {
	handler  http.Handler
	mutex    sync.Mutex
	stopping bool
	active   sync.WaitGroup
}

func (tracker *requestTracker) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	tracker.mutex.Lock()
	if tracker.stopping {
		tracker.mutex.Unlock()
		writer.Header().Set("Connection", "close")
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		writer.Header().Set(protocol.VersionHeader, "1")
		writer.Header().Set("Cache-Control", "no-store")
		writer.WriteHeader(http.StatusServiceUnavailable)
		writer.Write([]byte(`{"protocol_version":1,"error":{"code":"INTERNAL_ERROR","message":"Agent Relay is shutting down; retry shortly."}}`))
		return
	}
	tracker.active.Add(1)
	tracker.mutex.Unlock()
	defer tracker.active.Done()
	tracker.handler.ServeHTTP(writer, request)
}

func (tracker *requestTracker) stop() {
	tracker.mutex.Lock()
	defer tracker.mutex.Unlock()
	tracker.stopping = true
}
