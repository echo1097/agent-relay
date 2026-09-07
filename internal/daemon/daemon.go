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
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return true, file.Close()
	}
	return false, errors.Join(err, file.Close())
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
	if err := writeRuntime(file, Runtime{Address: listener.Addr().String(), NodeID: options.Node.ID, Version: options.Version}); err != nil {
		return errors.Join(err, listener.Close(), file.Close())
	}
	httpCtx, cancelHTTP := context.WithCancel(context.Background())
	defer cancelHTTP()
	server.BaseContext = func(net.Listener) context.Context { return httpCtx }
	requests := &requestTracker{handler: server.Handler}
	server.Handler = requests
	serveErrors := make(chan error, 1)
	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		serveErrors <- server.Serve(listener)
	}()
	backgroundCtx, stopBackground := context.WithCancel(ctx)
	backgroundDone := make(chan struct{})
	deliveryDone := make(chan struct{})
	defer func() {
		requests.stop()
		stopBackground()
		shutdownCtx, stopHTTP := context.WithTimeout(context.Background(), 5*time.Second)
		shutdownErr := server.Shutdown(shutdownCtx)
		stopHTTP()
		if shutdownErr != nil {
			cancelHTTP()
			shutdownErr = errors.Join(shutdownErr, server.Close())
		}
		requests.active.Wait()
		<-serveDone
		<-backgroundDone
		<-deliveryDone
		returnErr = errors.Join(returnErr, shutdownErr)
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		returnErr = errors.Join(returnErr, registry.OfflineAll(cleanupCtx), file.Close())
		logger.Info("daemon stopped")
	}()
	go func() {
		defer close(backgroundDone)
		if options.Background != nil {
			options.Background(backgroundCtx)
		}
	}()
	go func() {
		defer close(deliveryDone)
		if options.Delivery != nil {
			options.Delivery.Run(backgroundCtx)
		}
	}()
	logger.Info("daemon started", "pid", os.Getpid(), "address", listener.Addr().String())
	if options.Ready != nil {
		options.Ready(listener.Addr())
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if options.Delivery != nil {
			if err := options.Delivery.Store.ExpireDeliveries(ctx, time.Now().UTC()); err != nil && ctx.Err() == nil {
				return err
			}
		}
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
