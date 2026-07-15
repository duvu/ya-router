package control

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
)

// Service owns isolated local and optional remote control listeners.
type Service struct {
	config        ListenerConfig
	localHandler  http.Handler
	remoteHandler http.Handler

	mu        sync.Mutex
	servers   []*http.Server
	listeners []net.Listener
	errors    chan error
	started   bool
}

func NewService(config ListenerConfig, localHandler, remoteHandler http.Handler) (*Service, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if config.UnixMode == 0 {
		config.UnixMode = 0o600
	}
	if config.UnixSocket != "" && localHandler == nil {
		return nil, fmt.Errorf("local control handler is required")
	}
	if config.RemoteAddress != "" && remoteHandler == nil {
		return nil, fmt.Errorf("remote control handler is required")
	}
	return &Service{
		config:        config,
		localHandler:  localHandler,
		remoteHandler: remoteHandler,
		errors:        make(chan error, 2),
	}, nil
}

func (service *Service) Start() error {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.started {
		return fmt.Errorf("control service is already started")
	}
	if service.config.UnixSocket != "" {
		if err := prepareUnixSocket(service.config.UnixSocket); err != nil {
			return err
		}
		listener, err := net.Listen("unix", service.config.UnixSocket)
		if err != nil {
			return fmt.Errorf("listen on control Unix socket: %w", err)
		}
		if err := os.Chmod(service.config.UnixSocket, service.config.UnixMode); err != nil {
			_ = listener.Close()
			_ = os.Remove(service.config.UnixSocket)
			return fmt.Errorf("set control Unix socket permissions: %w", err)
		}
		server := service.newHTTPServer(service.localHandler)
		service.servers = append(service.servers, server)
		service.listeners = append(service.listeners, listener)
		service.serve(server, listener, "unix")
	}
	if service.config.RemoteAddress != "" {
		tlsConfig, err := service.config.tlsConfig()
		if err != nil {
			service.closeStartedLocked()
			return err
		}
		listener, err := net.Listen("tcp", service.config.RemoteAddress)
		if err != nil {
			service.closeStartedLocked()
			return fmt.Errorf("listen on remote control address: %w", err)
		}
		tlsListener := tls.NewListener(listener, tlsConfig)
		server := service.newHTTPServer(service.remoteHandler)
		service.servers = append(service.servers, server)
		service.listeners = append(service.listeners, tlsListener)
		service.serve(server, tlsListener, "remote")
	}
	service.started = true
	return nil
}

func (service *Service) newHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:      handler,
		ReadTimeout:  service.config.ReadTimeout,
		WriteTimeout: service.config.WriteTimeout,
		IdleTimeout:  service.config.IdleTimeout,
	}
}

func (service *Service) serve(server *http.Server, listener net.Listener, name string) {
	go func() {
		err := server.Serve(listener)
		if err == nil || errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
			return
		}
		select {
		case service.errors <- fmt.Errorf("%s control listener failed: %w", name, err):
		default:
		}
	}()
}

func (service *Service) Errors() <-chan error { return service.errors }

func (service *Service) Shutdown(ctx context.Context) error {
	service.mu.Lock()
	servers := append([]*http.Server(nil), service.servers...)
	listeners := append([]net.Listener(nil), service.listeners...)
	service.mu.Unlock()

	var combined error
	for _, server := range servers {
		if err := server.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			combined = errors.Join(combined, err)
		}
	}
	for _, listener := range listeners {
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			combined = errors.Join(combined, err)
		}
	}
	if service.config.UnixSocket != "" {
		if err := os.Remove(service.config.UnixSocket); err != nil && !os.IsNotExist(err) {
			combined = errors.Join(combined, err)
		}
	}
	return combined
}

func (service *Service) closeStartedLocked() {
	for _, listener := range service.listeners {
		_ = listener.Close()
	}
	if service.config.UnixSocket != "" {
		_ = os.Remove(service.config.UnixSocket)
	}
	service.servers = nil
	service.listeners = nil
}
