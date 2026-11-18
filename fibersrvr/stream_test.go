package fibersrvr_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

// blockingStream yields one chunk and then blocks until release closes,
// like a server-sent event stream.
type blockingStream struct {
	sent    bool
	release <-chan struct{}
}

func (s *blockingStream) Read(p []byte) (int, error) {
	if !s.sent {
		s.sent = true

		return copy(p, "data: one\n\n"), nil
	}

	<-s.release

	return 0, io.EOF
}

// TestFiberStreamNotBuffered: the access stage must not read a streamed
// body to measure it; the client gets the first chunk while the stream is
// still open.
func TestFiberStreamNotBuffered(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	defer close(release)

	srv, _ := newServer(t)
	if err := srv.Register(routes(func(r fiber.Router) {
		r.Get("/events", func(c fiber.Ctx) error {
			return c.SendStream(&blockingStream{release: release})
		})
	})); err != nil {
		t.Fatal(err)
	}

	base := start(t, srv)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/events", nil)

	res, err := clientFor(req.URL.String()).Do(req)
	if err != nil {
		t.Fatalf("no response while the stream is open: %v", err)
	}
	defer res.Body.Close()

	buf := make([]byte, 11)
	if _, err := io.ReadFull(res.Body, buf); err != nil || string(buf) != "data: one\n\n" {
		t.Fatalf("first chunk %q, %v", buf, err)
	}
}

// TestFiberStreamThenError: an error after a body stream started keeps
// the stream, and the failed record says the response had started.
func TestFiberStreamThenError(t *testing.T) {
	t.Parallel()

	srv, logs := newServer(t)
	res := serve(t, srv, routes(func(r fiber.Router) {
		r.Get("/s", func(c fiber.Ctx) error {
			_ = c.SendStream(strings.NewReader("streamed"))

			return errors.New("late")
		})
	}), http.MethodGet, "/s", nil, nil)

	recs := logs.access(t)
	if res.body != "streamed" || len(recs) != 1 || recs[0]["srvr.response.committed"] != true {
		t.Fatalf("response %q, records %v", res.body, recs)
	}
}
