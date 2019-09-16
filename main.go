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
	"github.com/hashicorp/hcl2/hcl"
	"github.com/spf13/pflag"
)

// config configures the upstream servers which are reachable at a given path.
// The key of the map is the path, the value the upstream URL.
//
// For example, using the path `/foo` and the URL `https://example.com/bar`,
// requesting `/foo/x.tar.gz` would request the URL
// `https://example.com/bar/x.tar.gz` in the background.
var config = map[string]string{
	"/debian":           "https://deb.debian.org/debian",
	"/debian-security":  "https://deb.debian.org/debian-security",
	"/centos":           "https://ftp.halifax.rwth-aachen.de/centos",
	"/centos-vault":     "http://vault.centos.org",
	"/centos-debuginfo": "http://debuginfo.centos.org",
	"/centos-epel":      "https://mirror.netcologne.de/fedora-epel",
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

// Options collects values parsed from command-line flags
type Options struct {
	EnableTLS       bool
	CertificateFile string
	KeyFile         string
	ConfigFile      string
}

func parseConfigOptions() Config {
	var opts Options

	flags := pflag.NewFlagSet("distriproxy", pflag.ContinueOnError)
	flags.BoolVar(&opts.EnableTLS, "enable-tls", false, "Run a TLS service (requires key and cert paths)")
	flags.StringVar(&opts.CertificateFile, "certificate", "", "Load TLS certificate from `filename`")
	flags.StringVar(&opts.KeyFile, "key", "", "Load TLS key from `filename`")
	flags.StringVar(&opts.ConfigFile, "config", "distriproxy.conf", "Load config from `filename`")

	err := flags.Parse(os.Args)
	if err == pflag.ErrHelp {
		os.Exit(0)
	}

	if err != nil {
		log.Printf("%v, exiting", err)
		os.Exit(1)
	}

	if len(flags.Args()) != 1 {
		log.Printf("additional arguments passed to distriproxy: %v, exiting", flags.Args()[1:])
		os.Exit(2)
	}

	cfg, err := ParseConfig(opts.ConfigFile)
	if err != nil {
		if e, ok := err.(hcl.Diagnostics); ok {
			for _, diag := range e.Errs() {
				log.Println(diag)
			}
		} else {
			log.Print(err)
		}
		os.Exit(3)
	}

	// cli flags overwrite config file entries
	if flags.Changed("enable-tls") {
		cfg.TLSEnable = &opts.EnableTLS
	}

	if flags.Changed("certificate") {
		cfg.TLSCertificateFile = &opts.CertificateFile
	}

	if flags.Changed("key") {
		cfg.TLSKeyFile = &opts.KeyFile
	}

	if cfg.TLSEnable != nil && *cfg.TLSEnable {
		if cfg.TLSCertificateFile == nil || *cfg.TLSCertificateFile == "" {
			log.Printf("error: TLS enabled but --certificate not set, exiting")
			os.Exit(1)
		}

		if cfg.TLSKeyFile == nil || *cfg.TLSKeyFile == "" {
			log.Printf("error: TLS enabled but --key not set, exiting")
			os.Exit(1)
		}
	}

	// make sure we have a valid default value
	if cfg.TLSEnable == nil {
		var enable = false
		cfg.TLSEnable = &enable
	}

	return cfg
}

func main() {
	// remove timestamp from logger
	log.SetFlags(0)
	log.SetOutput(os.Stderr)

	cfg := parseConfigOptions()

	mux := http.NewServeMux()

	// make a copy of the default HTTP client to use by the proxy instances
	var client = *http.DefaultClient

	for prefix, url := range config {
		mux.Handle(prefix+"/", NewProxy(prefix, url, &client))
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

		log.Printf("listening on %v (TLS %v)", listener.Addr(), *cfg.TLSEnable)
	case 1:
		// one listener supplied by systemd, use that one
		listener = listeners[0]
		log.Printf("listening on %v via systemd socket activation (TLS %v)", listener.Addr(), *cfg.TLSEnable)
	default:
		log.Printf("got %d listeners, expected one", len(listeners))
		os.Exit(1)
	}

	done := gracefulShutdown(&srv)
	if *cfg.TLSEnable {
		err = srv.ServeTLS(listener, *cfg.TLSCertificateFile, *cfg.TLSKeyFile)
	} else {
		err = srv.Serve(listener)
	}
	if err == http.ErrServerClosed {
		log.Printf("waiting for graceful shutdown")
		<-done
		log.Printf("shutdown completed")
		err = nil
	}

	if err != nil {
		log.Printf("Serve returned error: %v", err)
		os.Exit(1)
	}
}
