package main

import (
	"log"
	"net/http"
)

var config = map[string]string{
	"/debian":            "https://ftp.halifax.rwth-aachen.de/debian",
	"/debian-security":   "http://security.debian.org/debian-security",
	"/centos/":           "https://ftp.halifax.rwth-aachen.de/centos",
	"/centos-vault/":     "http://vault.centos.org",
	"/centos-debuginfo/": "http://debuginfo.centos.org",
}

func main() {
	mux := http.NewServeMux()

	// make a copy of the default HTTP client to use by the proxy instances
	var client = *http.DefaultClient

	for prefix, url := range config {
		mux.Handle(prefix+"/", http.StripPrefix(prefix, NewRewriteProxy(url, &client)))
	}

	// install catch-all handler to log invalid requests
	mux.HandleFunc("/", func(rw http.ResponseWriter, req *http.Request) {
		log.Printf("%v -> 404 not found", req.URL.Path)
		rw.Header().Set("Server", "distriproxy")
		rw.WriteHeader(http.StatusNotFound)
	})

	srv := http.Server{
		Addr:    ":8080",
		Handler: RejectProxyRequests(mux),
	}

	log.Printf("listening on %v", srv.Addr)

	log.Fatal(srv.ListenAndServe())
}
