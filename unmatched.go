package srvr

import (
	"bytes"
	"cmp"
	"net/http"

	"github.com/kartaladev/prblm"
)

// answerUnmatched returns the router stage: matched requests go to mux;
// for unmatched ones, ServeMux's plain-text 404 and 405 become problems
// (the 405 keeps ServeMux's Allow header), and any other fallback answer,
// such as a redirect to the canonical path, passes through unchanged.
//
// A matched request is dispatched through mux.ServeHTTP, never through the
// handler mux.Handler returns, because only ServeHTTP sets r.Pattern and
// the path values.
func (s *Server) answerUnmatched(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pattern := mux.Handler(r); pattern != "" {
			mux.ServeHTTP(w, r)

			return
		}

		rec := &fallbackRecorder{header: http.Header{}}
		mux.ServeHTTP(rec, r)

		switch rec.status {
		case http.StatusNotFound:
			s.problems.Write(w, r, prblm.ErrNotFound)
		case http.StatusMethodNotAllowed:
			s.problems.Write(w, r, prblm.New(http.StatusMethodNotAllowed, prblm.WithHeader("Allow", rec.header.Get("Allow"))))
		default:
			rec.replay(w)
		}
	})
}

// fallbackRecorder records what ServeMux's fallback handler writes.
type fallbackRecorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (f *fallbackRecorder) Header() http.Header { return f.header }

func (f *fallbackRecorder) WriteHeader(code int) {
	if f.status == 0 {
		f.status = code
	}
}

func (f *fallbackRecorder) Write(b []byte) (int, error) {
	f.WriteHeader(http.StatusOK)

	return f.body.Write(b)
}

// replay writes the recorded response to w.
func (f *fallbackRecorder) replay(w http.ResponseWriter) {
	for k, v := range f.header {
		w.Header()[k] = v
	}

	w.WriteHeader(cmp.Or(f.status, http.StatusOK))
	_, _ = w.Write(f.body.Bytes())
}
