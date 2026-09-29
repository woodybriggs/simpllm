package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/woodybriggs/simpllm/internal/activation"
	"github.com/woodybriggs/simpllm/internal/config"
	"github.com/woodybriggs/simpllm/internal/proxy"
)

func main() {
	// Handle subcommands before flag parsing.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "test":
			testCmd(os.Args[2:])
			return
		case "secret":
			secretCmd(os.Args[2:])
			return
		}
	}

	configPath := flag.String("config", "", "path to config file (disables auto-merge)")
	noWatch := flag.Bool("no-watch", false, "disable config file watching")
	flag.Parse()

	if err := run(*configPath, *noWatch); err != nil {
		log.Fatalf("simpllm: %v", err)
	}
}

// hotHandler wraps the current handler and allows atomic swaps.
type hotHandler struct {
	mu      sync.RWMutex
	handler http.Handler
}

func (h *hotHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	hh := h.handler
	h.mu.RUnlock()
	hh.ServeHTTP(w, r)
}

func (h *hotHandler) Swap(handler http.Handler) {
	h.mu.Lock()
	h.handler = handler
	h.mu.Unlock()
}

func run(configPath string, noWatch bool) error {
	// ── Config watcher ─────────────────────────────────────────
	var watcher *config.Watcher
	if !noWatch {
		paths := config.ConfigPaths(configPath)
		var err error
		watcher, err = config.NewWatcher(paths)
		if err != nil {
			return err
		}
		defer watcher.Close()
	}

	// ── OS signals ─────────────────────────────────────────────
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	hh := &hotHandler{}
	var servers []*http.Server
	var listeners []net.Listener
	var unixPath string

	reload := func() error {
		// Load new config.
		cfg, err := config.Load(configPath)
		if err != nil {
			log.Printf("ERROR config reload failed: %v — keeping current config", err)
			return nil
		}

		log.Printf("config loaded: models=%v", cfg.ModelNames())

		// Create new handler and swap it in.
		hh.Swap(proxy.NewServer(cfg))

		// Only create listeners on first call.
		if len(listeners) == 0 {
			// Try systemd socket-activated listeners first.
			listeners = activation.Listeners()
			if len(listeners) > 0 {
				log.Printf("socket activation: inherited %d listener(s) from systemd", len(listeners))
			} else {
				// Manual binding — create listeners from config.
				if cfg.Listen.HTTP != "" {
					ln, err := net.Listen("tcp", cfg.Listen.HTTP)
					if err != nil {
						log.Printf("ERROR http listen %s: %v", cfg.Listen.HTTP, err)
					} else {
						listeners = append(listeners, ln)
						log.Printf("  http: listening on %s", cfg.Listen.HTTP)
					}
				}

				if cfg.Listen.Unix != "" {
					os.Remove(cfg.Listen.Unix)
					ln, err := net.Listen("unix", cfg.Listen.Unix)
					if err != nil {
						log.Printf("ERROR unix listen %s: %v", cfg.Listen.Unix, err)
					} else {
						listeners = append(listeners, ln)
						unixPath = cfg.Listen.Unix
						log.Printf("  unix: listening on %s", cfg.Listen.Unix)
					}
				}
			}

			// Start serving on each listener. The hotHandler is shared
			// so new requests always use the latest config.
			for _, ln := range listeners {
				srv := &http.Server{
					Handler:           hh,
					ReadHeaderTimeout: 10 * time.Second,
					ReadTimeout:       5 * time.Minute,
					WriteTimeout:      0, // no timeout for streaming
					IdleTimeout:       120 * time.Second,
				}
				servers = append(servers, srv)
				go func(addr string) {
					if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
						log.Printf("server error (%s): %v", addr, err)
					}
				}(ln.Addr().String())
			}
		}

		log.Printf("simpllm ready")
		return nil
	}

	// ── Initial start ──────────────────────────────────────────
	if err := reload(); err != nil {
		return err
	}

	// ── Wait loop ──────────────────────────────────────────────
	if watcher != nil {
		for {
			select {
			case <-watcher.Changes():
				if err := reload(); err != nil {
					return err
				}
			case sig := <-sigs:
				return shutdown(servers, unixPath, sig)
			}
		}
	}

	// No watcher — just wait for OS signal.
	sig := <-sigs
	return shutdown(servers, unixPath, sig)
}

func shutdown(servers []*http.Server, unixPath string, sig os.Signal) error {
	log.Printf("received %s, shutting down...", sig)

	for _, srv := range servers {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("shutdown error: %v", err)
		}
	}

	if unixPath != "" {
		os.Remove(unixPath)
	}

	log.Printf("goodbye")
	return nil
}
