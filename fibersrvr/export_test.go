package fibersrvr

import (
	"net/http"
	"sync"

	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/srvr"
)

// configured guards the configure phase of servers driven by Test.
var configured sync.Map

// Test configures srv with confs on first use and answers req through the
// app with fiber's app.Test, without a listener.
func (s *Server) Test(req *http.Request, confs ...srvr.RouteConfigurer[fiber.Router]) (*http.Response, error) {
	if _, done := configured.LoadOrStore(s, true); !done {
		if _, err := s.configure(confs); err != nil {
			return nil, err
		}
	}

	return s.app.Test(req, fiber.TestConfig{Timeout: 0})
}

// App returns the server's fiber app.
func (s *Server) App() *fiber.App { return s.app }

// OpenConns returns the number of server connections still open, or -1
// before the server served.
func (s *Server) OpenConns() int {
	r := s.serving.Load()
	if r == nil {
		return -1
	}

	r.conns.mu.Lock()
	defer r.conns.mu.Unlock()

	return len(r.conns.open)
}
