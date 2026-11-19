package fibersrvr

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/engine"
)

// structFS is a file system of a struct type that is not comparable, as
// many consumer wrappers are: fiber cannot tell two such values apart.
type structFS struct {
	base   fs.FS
	labels map[string]string
	opens  *atomic.Int64
	// failAfter makes Open fail with fs.ErrNotExist once it was called
	// that many times; 0 never fails.
	failAfter int64
}

func (f structFS) Open(name string) (fs.File, error) {
	if n := f.opens.Add(1); f.failAfter > 0 && n > f.failAfter {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}

	return f.base.Open(name)
}

// get answers GET target through srv's app with app.Test.
func get(t *testing.T, srv *Server, target string, confs ...srvr.RouteConfigurer[fiber.Router]) int {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}

	res, err := srv.Test(req, confs...)
	if err != nil {
		t.Fatal(err)
	}

	_ = res.Body.Close()

	return res.StatusCode
}

// TestStatic_SendFileHandlers checks that a mount keeps one fiber file
// handler whatever its file system's type: fiber compares SendFile
// configurations by their file system and cannot match a value of a
// non-comparable struct type, so without care every request would build
// a handler, and the app keep up to 64 of them.
func TestStatic_SendFileHandlers(t *testing.T) {
	t.Parallel()

	site := fstest.MapFS{"app.js": {Data: []byte("console.log(1)")}}
	srv := newInternalServer(t)
	conf := Static("/", structFS{base: site, labels: map[string]string{}, opens: new(atomic.Int64)})

	for range 5 {
		if status := get(t, srv, "/app.js", conf); status != http.StatusOK {
			t.Fatalf("GET /app.js = %d, want 200", status)
		}
	}

	if n := reflect.ValueOf(srv.app).Elem().FieldByName("sendfiles").Len(); n != 1 {
		t.Errorf("the app keeps %d file handlers after 5 requests to one mount, want 1", n)
	}
}

// recordingLogger is a fasthttp logger that keeps what it is given.
type recordingLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *recordingLogger) Printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *recordingLogger) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return strings.Join(l.lines, "\n")
}

// TestStatic_VanishLogger checks where fasthttp's file server logs when a
// file vanishes between the mount's resolution and SendFile (design D2,
// Risks; task 5.5): through the fasthttp server's own logger, which fiber
// sets to one that discards everything, so nothing reaches stdout or
// stderr.
func TestStatic_VanishLogger(t *testing.T) {
	t.Parallel()

	site := fstest.MapFS{"app.js": {Data: []byte("console.log(1)")}}
	conf := Static("/", structFS{base: site, opens: new(atomic.Int64), failAfter: 2}) // fs.Stat and the mount open it first

	srv := newInternalServer(t)
	if got := fmt.Sprintf("%T", srv.app.Server().Logger); got != "*fiber.disableLogger" {
		t.Errorf("fasthttp server logger = %s, want fiber's *fiber.disableLogger, which discards", got)
	}

	logger := &recordingLogger{}
	srv.app.Server().Logger = logger

	if status := get(t, srv, "/app.js", conf); status != http.StatusNotFound {
		t.Fatalf("GET /app.js = %d, want 404", status)
	}

	if !strings.Contains(logger.String(), "cannot open file") {
		t.Errorf("fasthttp server logger got %q, want the file server's open failure", logger.String())
	}
}

// TestDotSegment covers the check of the path a client sent for "." and
// ".." elements fiber's router removes before routing, including escapes
// cut short at the end of the path.
func TestDotSegment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw  string
		want bool
	}{
		{"/", false},
		{"/assets/app.js", false},
		{"/.env", false},
		{"/...", false},
		{"/a/./b", true},
		{"/a/..", true},
		{"/%2e%2e/secret", true},
		{"/%2E/x", true},
		{"/a%2F..%2Fb", true},
		{"/%252e%252e/x", false},
		{"/%", false},
		{"/a%2", false},
		{"/%zz/..", true},
		{"/caf%C3%A9.txt", false},
	}

	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			t.Parallel()

			if got := dotSegment([]byte(tt.raw)); got != tt.want {
				t.Errorf("dotSegment(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

// TestMalformedEscape covers the check of the path a client sent for an
// escape that is not "%" and two hexadecimal digits, which a mount looks
// for when fiber's UnescapePath decoded the path it routes (static-files:
// Hostile paths), at and one short of the end of the path.
func TestMalformedEscape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw  string
		want bool
	}{
		{"/assets/app.js", false},
		{"/assets/a%20b.txt", false},
		{"/assets/caf%C3%a9.txt", false},
		{"/assets/a%2F", false},
		{"/assets/%zz", true},
		{"/assets/%2z", true},
		{"/assets/%z2", true},
		{"/assets/a%2", true},
		{"/assets/a%", true},
		{"/assets/%20%zz", true},
		{"/assets/%2520", false},
	}

	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			t.Parallel()

			if got := malformedEscape([]byte(tt.raw)); got != tt.want {
				t.Errorf("malformedEscape(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

// TestStatic_ServeAnswers checks the mount's answers that its GET-only
// routes and fiber v3.7.0's SendFile never produce, but a direct route or
// another fiber version could: a method other than GET and HEAD gets the
// 405 problem with Allow: GET, HEAD, and an error of SendFile other than
// its 404 fails the request.
func TestStatic_ServeAnswers(t *testing.T) {
	t.Parallel()

	m, err := engine.NewStatic("/", fstest.MapFS{"app.js": {Data: []byte("console.log(1)")}})
	if err != nil {
		t.Fatal(err)
	}

	sm := &staticMount{mount: m, decode: true}
	failure := errors.New("sendfile failed")

	tests := []struct {
		name    string
		handler func(fiber.Ctx) error
		// want is the error the handler returns, matched with errors.Is;
		// nil for the 405 problem with Allow: GET, HEAD.
		want error
	}{
		{"POST", sm.serve, nil},
		{"SendFile's 404", func(c fiber.Ctx) error { return sendFileFailure(c, fiber.NewError(http.StatusNotFound, "gone")) }, prblm.ErrNotFound},
		{"another SendFile error", func(c fiber.Ctx) error { return sendFileFailure(c, failure) }, failure},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got error

			app := fiber.New()
			app.Post("/*", func(c fiber.Ctx) error { got = tt.handler(c); return nil })

			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "/app.js", nil)
			if err != nil {
				t.Fatal(err)
			}

			res, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}

			_ = res.Body.Close()

			if tt.want != nil {
				if !errors.Is(got, tt.want) {
					t.Errorf("handler = %v, want %v", got, tt.want)
				}

				return
			}

			if p, ok := errors.AsType[*prblm.Problem](got); !ok || p.Status != http.StatusMethodNotAllowed || p.Header.Get("Allow") != "GET, HEAD" {
				t.Errorf("serve = %v, want the 405 problem with Allow: GET, HEAD", got)
			}
		})
	}
}
