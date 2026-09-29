package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

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
	flag.Parse()

	// Load config with merge support.
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	log.Printf("simpllm starting")
	log.Printf("  models: %v", cfg.ModelNames())

	// Create the handler.
	srv := proxy.NewServer(cfg)

	// Shared server config for all transports.
	server := &http.Server{
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute, // long for streaming
		WriteTimeout:      0,               // no timeout — streaming responses need it
		IdleTimeout:       120 * time.Second,
	}

	var listeners []net.Listener
	var unixPath string
	launchdSocket := false

	// Start HTTP listener.
	if cfg.Listen.HTTP != "" {
		ln, err := net.Listen("tcp", cfg.Listen.HTTP)
		if err != nil {
			log.Fatalf("http listen: %v", err)
		}
		listeners = append(listeners, ln)
		go func() {
			log.Printf("  http: listening on %s", cfg.Listen.HTTP)
			if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
				log.Printf("http server error: %v", err)
			}
		}()
	}

	// Start Unix socket listener.
	if cfg.Listen.Unix != "" {
		// Try to inherit socket from launchd or systemd (FD 3).
		// Both systems use the same mechanism: socket activation passes
		// the listening socket as file descriptor 3.
		ln := tryInheritedSocket()

		if ln != nil {
			unixPath = cfg.Listen.Unix
			launchdSocket = true
			listeners = append(listeners, ln)
			go func() {
				log.Printf("  unix: inherited socket, serving on %s", cfg.Listen.Unix)
				if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
					log.Printf("unix server error: %v", err)
				}
			}()
		} else {
			// No launchd socket — create one ourselves.
			os.Remove(cfg.Listen.Unix)

			ln, err := net.Listen("unix", cfg.Listen.Unix)
			if err != nil {
				log.Fatalf("unix listen: %v", err)
			}
			unixPath = cfg.Listen.Unix
			listeners = append(listeners, ln)
			go func() {
				log.Printf("  unix: listening on %s", cfg.Listen.Unix)
				if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
					log.Printf("unix server error: %v", err)
				}
			}()
		}
	}

	if len(listeners) == 0 {
		log.Fatal("no listeners configured — set listen.http or listen.unix")
	}

	// Wait for interrupt signal.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	log.Printf("received %s, shutting down...", sig)

	// Graceful shutdown: wait up to 10s for in-flight requests to complete.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("shutdown error: %v", err)
	}

	// Clean up unix socket (only if we created it, not when inherited from launchd).
	if unixPath != "" && !launchdSocket {
		os.Remove(unixPath)
	}

	log.Printf("goodbye")
}

// tryInheritedSocket attempts to inherit a socket from launchd or systemd (FD 3).
// Both systems use socket activation: they create the socket and pass the file
// descriptor to the process. If no socket was provided, this returns nil and
// the caller creates one normally.
func tryInheritedSocket() net.Listener {
	f := os.NewFile(3, "launchd-socket")
	if f == nil {
		return nil
	}

	ln, err := net.FileListener(f)
	if err != nil {
		f.Close()
		return nil
	}
	return ln
}
