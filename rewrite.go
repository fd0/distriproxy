package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"golang.org/x/net/context/ctxhttp"
)

// RewriteProxy forwards requests repositories to an upstream server.
type RewriteProxy struct {
	Source string
	Client *http.Client
}

// NewRewriteProxy initializes a new proxy repositories using upstream as
// the source url for packages and files. If no http.Client is provided,
// http.DefaultClient is used.
func NewRewriteProxy(upstream string, client *http.Client) *RewriteProxy {
	// use the default client if none is provided
	if client == nil {
		client = http.DefaultClient
	}

	// strip trailing slash, it is added back in the handler below
	upstream = strings.TrimRight(upstream, "/")

	return &RewriteProxy{
		Source: upstream,
		Client: client,
	}
}

func (p *RewriteProxy) log(req *http.Request, msg string, args ...interface{}) {
	prefix := fmt.Sprintf("%v %v %v ", req.RemoteAddr, req.Method, req.URL.Path)
	log.Printf(prefix+msg, args...)
}

// filterHeadersToUpstream contains request header names that are not sent to
// the upstream server.
var filterHeadersToUpstream = map[string]struct{}{
	"Connection": struct{}{},
	"Host":       struct{}{},
}

func (p *RewriteProxy) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
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
