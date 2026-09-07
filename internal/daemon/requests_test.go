package daemon

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestShutdownRejectsNewWorkAndDrainsActiveRequests(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	tracker := &requestTracker{handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		close(started)
		<-release
		writer.WriteHeader(http.StatusNoContent)
	})}
	go func() {
		defer close(finished)
		tracker.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/health", nil))
	}()
	<-started
	tracker.stop()
	writer := httptest.NewRecorder()
	tracker.ServeHTTP(writer, httptest.NewRequest("GET", "/v1/health", nil))
	if writer.Code != http.StatusServiceUnavailable {
		t.Fatal(writer.Code)
	}
	drained := make(chan struct{})
	go func() { tracker.active.Wait(); close(drained) }()
	select {
	case <-drained:
		t.Fatal("active request was not drained")
	default:
	}
	close(release)
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("request did not finish")
	}
	<-finished
}
