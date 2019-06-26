package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"golang.org/x/net/context/ctxhttp"
)

// RejectProxyRequests rejects requests which are detected as proxy requests or
// have HTTP methods other than GET/HEAD. For all other requests, the handler
// next is called.
func RejectProxyRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		// reject proxy requests
		if req.URL.Host != "" {
			log.Printf("%v reject proxy request for %v", req.RemoteAddr, req.URL)

			rw.Header().Set("Server", "distriproxy")
			rw.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(rw, "this is not a proxy\n")
			return
		}

		// only allow GET and HEAD
		if req.Method != http.MethodGet && req.Method != http.MethodHead {
			log.Printf("%v reject invalid method", req.RemoteAddr)

			rw.Header().Set("Server", "distriproxy")
			rw.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		// otherwise pass the request to the next handler
		next.ServeHTTP(rw, req)
	})
}

// Proxy forwards requests repositories to an upstream server.
type Proxy struct {
	Name   string
	Source string
	Client *http.Client
}

// NewProxy initializes a new proxy repositories using upstream as
// the source url for packages and files. If no http.Client is provided,
// http.DefaultClient is used.
func NewProxy(prefix, upstream string, client *http.Client) http.Handler {
	// use the default client if none is provided
	if client == nil {
		client = http.DefaultClient
	}

	// strip trailing slash, it is added back in the handler below
	upstream = strings.TrimRight(upstream, "/")

	p := &Proxy{
		Name:   prefix,
		Source: upstream,
		Client: client,
	}

	return http.StripPrefix(prefix, p)
}

func (p *Proxy) log(req *http.Request, msg string, args ...interface{}) {
	prefix := fmt.Sprintf("%v %v %v %v ", p.Name, req.RemoteAddr, req.Method, req.URL.Path)
	log.Printf(prefix+msg, args...)
}

// filterHeadersToUpstream contains request header names that are not sent to
// the upstream server.
var filterHeadersToUpstream = map[string]struct{}{
	"Connection": struct{}{},
	"Host":       struct{}{},
}

func (p *Proxy) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	upstreamURL := p.Source + req.URL.Path
	upstreamReq, err := http.NewRequest(req.Method, upstreamURL, nil)
	if err != nil {
		p.log(req, "constructing upstream request failed: %v", err)
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}

	// copy some headers from incoming request to upstream request
	for name, values := range req.Header {
		if _, ok := filterHeadersToUpstream[name]; ok {
			continue
		}
		upstreamReq.Header[name] = values
	}

	res, err := ctxhttp.Do(req.Context(), p.Client, upstreamReq)
	if err != nil {
		p.log(req, "upstream request failed: %v", err)
		rw.WriteHeader(http.StatusBadGateway)
		return
	}

	// copy header from response
	for name, values := range res.Header {
		rw.Header()[name] = values
	}

	rw.Header().Add("Via", "distriproxy")

	// send status
	rw.WriteHeader(res.StatusCode)

	// copy body to client
	_, err = io.Copy(rw, res.Body)
	if err != nil {
		p.log(req, "passing response failed: %v", err)
		_ = res.Body.Close()
		return
	}

	err = res.Body.Close()
	if err != nil {
		p.log(req, "closing upstream response body failed: %v", err)
		return
	}

	p.log(req, "---> %v", res.Status)
}
