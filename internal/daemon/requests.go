package daemon

import (
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
		http.Error(writer, "Agent Relay is shutting down; retry shortly.", http.StatusServiceUnavailable)
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
