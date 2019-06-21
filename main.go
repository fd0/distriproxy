package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/coreos/go-systemd/activation"
)

// config configures the upstream servers which are reachable at a given path.
// The key of the map is the path, the value the upstream URL.
//
// For example, using the path `/foo` and the URL `https://example.com/bar`,
// requesting `/foo/x.tar.gz` would request the URL
// `https://example.com/bar/x.tar.gz` in the background.
var config = map[string]string{
	"/debian":           "https://ftp.halifax.rwth-aachen.de/debian",
	"/debian-security":  "http://security.debian.org/debian-security",
	"/centos":           "https://ftp.halifax.rwth-aachen.de/centos",
	"/centos-vault":     "http://vault.centos.org",
	"/centos-debuginfo": "http://debuginfo.centos.org",
}

// wait ten seconds for clients to finish their business before shutting down
const shutdownTimeout = 10 * time.Second

func gracefulShutdown(srv *http.Server) <-chan struct{} {
	done := make(chan struct{})

	// install signal handler for INT and TERM
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		// wait for signal
		c := <-ch
		log.Printf("received %v, shutting down gracefully", c)

		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		err := srv.Shutdown(ctx)
		if err != nil {
			log.Fatalf("shutdown failed: %v", err)
		}
		close(done)
	}()

	return done
}

func main() {
	// remove timestamp from logger
	log.SetFlags(0)
	log.SetOutput(os.Stderr)

	mux := http.NewServeMux()

	// make a copy of the default HTTP client to use by the proxy instances
	var client = *http.DefaultClient

	for prefix, url := range config {
		mux.Handle(prefix+"/", http.StripPrefix(prefix, NewProxy(url, &client)))
	}

	// install catch-all handler to log invalid requests
	mux.HandleFunc("/", func(rw http.ResponseWriter, req *http.Request) {
		log.Printf("%v %v %v -> 404 not found", req.RemoteAddr, req.Method, req.URL.Path)
		rw.Header().Set("Server", "distriproxy")
		rw.WriteHeader(http.StatusNotFound)
	})

	srv := http.Server{
		Handler: RejectProxyRequests(mux),
	}

	var listener net.Listener

	// try systemd socket activation first
	listeners, err := activation.Listeners()
	if err != nil {
		panic(err)
	}

	switch len(listeners) {
	case 0:
		// no listeners found, listen manually
		listener, err = net.Listen("tcp", ":8080")
		if err != nil {
			log.Printf("unable to bind to port: %v", err)
			os.Exit(1)
		}

		log.Printf("listening on %v", listener.Addr())
	case 1:
		// one listener supplied by systemd, use that one
		listener = listeners[0]
		log.Printf("listening on %v via systemd socket activation", listener.Addr())
	default:
		log.Printf("got %d listeners, expected one", len(listeners))
		os.Exit(1)
	}

	done := gracefulShutdown(&srv)
	err = srv.Serve(listener)
	if err != nil && err != http.ErrServerClosed {
		log.Printf("Serve returned error: %v", err)
	}
	<-done
}
