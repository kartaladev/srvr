package paritytest

import (
	"net/http"
	"net/url"
	"sync"
	"testing"
)

// clients holds the HTTP client of every server the helpers started, by
// address. Each server has its own, so a test's cleanup closes only its
// own idle connections: closing those of http.DefaultClient, shared by
// parallel tests, broke another test's request in flight ("http:
// CloseIdleConnections called").
var clients sync.Map // string -> *http.Client

// registerClient gives the server at addr a client of its own; when the
// test ends, before the server stops (cleanups run last-in first-out), it
// closes that client's idle connections, which would otherwise hold the
// drain for up to 5s.
func registerClient(t *testing.T, addr string) {
	t.Helper()

	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConnsPerHost = 16 // parallel requests reuse connections instead of exhausting ports
	c := &http.Client{Transport: tr}
	clients.Store(addr, c)

	t.Cleanup(func() {
		c.CloseIdleConnections()
		clients.CompareAndDelete(addr, c)
	})
}

// clientFor returns the client of the server rawURL points at, or
// http.DefaultClient for a server the helpers did not start.
func clientFor(rawURL string) *http.Client {
	if u, err := url.Parse(rawURL); err == nil {
		if c, ok := clients.Load(u.Host); ok {
			return c.(*http.Client)
		}
	}

	return http.DefaultClient
}
