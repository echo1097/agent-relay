package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"syscall"
	"time"

	"agent-relay/internal/agents"
)

func openLock(path string) (*os.File, bool, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, false, err
	}
	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return nil, true, file.Close()
	}
	if err != nil {
		return nil, false, errors.Join(err, file.Close())
	}
	return file, false, nil
}

func Running(path string) (bool, error) {
	file, running, err := openLock(path)
	if err != nil || running {
		return running, err
	}
	return false, file.Close()
}

func Run(ctx context.Context, path string, logger *slog.Logger, registry *agents.Registry, options HTTPOptions) (returnErr error) {
	if registry == nil {
		return errors.New("daemon requires a local agent registry")
	}
	server, err := newHTTPServer(registry, options, logger)
	if err != nil {
		return err
	}
	if options.Address == "" {
		return errors.New("HTTP listen address is required")
	}
	file, running, err := openLock(path)
	if err != nil {
		return err
	}
	if running {
		return fmt.Errorf("daemon is already running for this application directory")
	}
	listener, err := net.Listen("tcp", options.Address)
	if err != nil {
		return errors.Join(err, file.Close())
	}
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()
	defer func() {
		httpCtx, stopHTTP := context.WithTimeout(context.Background(), 5*time.Second)
		shutdownErr := server.Shutdown(httpCtx)
		stopHTTP()
		if shutdownErr != nil {
			shutdownErr = errors.Join(shutdownErr, server.Close())
		}
		returnErr = errors.Join(returnErr, shutdownErr)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		returnErr = errors.Join(returnErr, registry.OfflineAll(shutdownCtx), file.Close())
		logger.Info("daemon stopped")
	}()
	logger.Info("daemon started", "pid", os.Getpid(), "address", listener.Addr().String())
	if options.Ready != nil {
		options.Ready(listener.Addr())
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if _, err := registry.Expire(ctx); err != nil && ctx.Err() == nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case err := <-serveErrors:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		case <-ticker.C:
		}
	}
}
