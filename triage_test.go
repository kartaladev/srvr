package srvr_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
)

// failedRecord serves GET /x with h and returns its failed record.
func failedRecord(t *testing.T, h srvr.HandlerFunc, opts ...srvr.Option) map[string]any {
	t.Helper()

	base, out := loggedServer(t, func(g *srvr.Group) { g.GET("/x", h) }, opts...)
	get(t, base+"/x")

	return out.waitRecord(t, "http.request.failed")
}

func returning(err error) srvr.HandlerFunc {
	return func(http.ResponseWriter, *http.Request) error { return err }
}

func chainOf(rec map[string]any) []string {
	raw, _ := rec["error.chain"].([]any)
	out := make([]string, len(raw))
	for i, v := range raw {
		out[i], _ = v.(string)
	}

	return out
}

// TestTriage_Description covers triage-error-records / Scenario: Error description.
func TestTriage_Description(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, typ, message string
		err                error
		chain              []string
	}{
		{"errors.New", "*errors.errorString", "boom", errors.New("boom"), []string{"*errors.errorString"}},
		{
			"net.OpError", "syscall.Errno", "insert user: dial: connection refused",
			fmt.Errorf("insert user: %w", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}),
			[]string{"*fmt.wrapError", "*net.OpError", "syscall.Errno"},
		},
		{"wrapped sentinel", "*errors.errorString", "a: prblm: not found", fmt.Errorf("a: %w", prblm.ErrNotFound), []string{"*fmt.wrapError", "*errors.errorString"}},
		{"joined", "*errors.joinError", "x\ny", errors.Join(errors.New("x"), errors.New("y")), []string{"*errors.joinError", "*errors.errorString", "*errors.errorString"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := failedRecord(t, returning(tt.err))
			if rec["error.type"] != tt.typ || rec["exception.message"] != tt.message || strings.Join(chainOf(rec), ",") != strings.Join(tt.chain, ",") {
				t.Fatalf("got type %v message %q chain %v", rec["error.type"], rec["exception.message"], chainOf(rec))
			}
		})
	}
}

// TestTriage_OwnLimits covers structured-logging / Scenario: Error attributes
// keep their own limits.
func TestTriage_OwnLimits(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct{ message, logged string }{
		{xs(5000), xs(5000)},
		{xs(9000), xs(8192) + cut},
	} {
		t.Run(strconv.Itoa(len(tt.message)), func(t *testing.T) {
			t.Parallel()

			if got := failedRecord(t, returning(errors.New(tt.message)))["exception.message"]; got != tt.logged {
				t.Fatalf("exception.message is %d bytes, want %d", len(fmt.Sprint(got)), len(tt.logged))
			}
		})
	}
}

// TestTriage_Problem covers triage-error-records / Scenario: Problem group.
func TestTriage_Problem(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		handler srvr.HandlerFunc
		problem map[string]any // nil: no problem group, committed true
		want    map[string]any // further attributes
	}{
		{"conflict", returning(prblm.ErrConflict), map[string]any{"status": float64(409), "type": "about:blank", "title": "Conflict"}, nil},
		{"boom", returning(errors.New("boom")), map[string]any{"status": float64(500), "title": "Internal Server Error"}, nil},
		{"late error", late(http.StatusOK, func() error { return errors.New("late") }), nil, nil},
		{"late panic", late(http.StatusOK, func() error { panic("late") }), nil, map[string]any{"error.kind": "panic", "level": "ERROR"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := failedRecord(t, tt.handler)
			p, hasProblem := rec["problem"].(map[string]any)
			if tt.problem == nil {
				if hasProblem || rec["srvr.response.committed"] != true {
					t.Fatalf("record = %v, want no problem and srvr.response.committed", rec)
				}
			}
			for k, v := range tt.problem {
				if p[k] != v {
					t.Fatalf("problem = %v, want %s %v", p, k, v)
				}
			}
			for k, v := range tt.want {
				if rec[k] != v {
					t.Fatalf("record = %v, want %s %v", rec, k, v)
				}
			}
		})
	}
}

// RegistrationHandler has a method used as a handler.
type RegistrationHandler struct{}

func (RegistrationHandler) Register(http.ResponseWriter, *http.Request) error {
	return errors.New("boom")
}

// Files is a mounted handler type.
type Files struct{}

func (*Files) ServeHTTP(http.ResponseWriter, *http.Request) { panic("files") }

// TestTriage_CodeLocation covers triage-error-records / Scenario: Handler location.
func TestTriage_CodeLocation(t *testing.T) {
	t.Parallel()

	routes := func(g *srvr.Group) {
		g.GET("/method", RegistrationHandler{}.Register)
		g.GET("/closure", func(http.ResponseWriter, *http.Request) error { return errors.New("boom") })
		g.Mount("/files/", &Files{})
	}

	tests := []struct{ path, function string }{
		{"/method", "github.com/kartaladev/srvr_test.RegistrationHandler.Register"},
		{"/closure", "github.com/kartaladev/srvr_test.TestTriage_CodeLocation.func1.1"},
		{"/files/a", "*srvr_test.Files.ServeHTTP"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()

			base, out := loggedServer(t, routes)
			get(t, base+tt.path)
			rec := out.waitRecord(t, "http.request.failed")
			// A method value resolves through generated code, so its file and
			// line are omitted; the others point at this file.
			hasFile := tt.path == "/method" || (strings.HasSuffix(fmt.Sprint(rec["code.file.path"]), "_test.go") && rec["code.line.number"] != nil)
			if rec["code.function.name"] != tt.function || !hasFile {
				t.Fatalf("code location = %v %v %v, want %s", rec["code.function.name"], rec["code.file.path"], rec["code.line.number"], tt.function)
			}
		})
	}
}

type panickyStringer struct{}

func (panickyStringer) String() string { panic("stringer") }

// TestTriage_Panic covers triage-error-records / Scenario: Panic record.
func TestTriage_Panic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, message, typ string
		value              func()
	}{
		{"string", "boom", "string", func() { panic("boom") }},
		{"error", "db down", "*errors.errorString", func() { panic(errors.New("db down")) }},
		{"nil map", "assignment to entry in nil map", "runtime.plainError", func() {
			var m map[string]int
			m["x"] = 1 //nolint:staticcheck // the row panics with a nil map write.
		}},
		{"panicking String", "<panic formatting value>", "srvr_test.panickyStringer", func() { panic(panickyStringer{}) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := failedRecord(t, func(http.ResponseWriter, *http.Request) error { tt.value(); return nil })
			if rec["level"] != "ERROR" || rec["error.kind"] != "panic" || rec["error.type"] != "panic" ||
				rec["exception.message"] != tt.message || rec["exception.type"] != tt.typ {
				t.Fatalf("record = %v", rec)
			}
			if st := fmt.Sprint(rec["exception.stacktrace"]); !strings.Contains(st, "TestTriage_Panic") || rec["srvr.stacktrace.truncated"] != false {
				t.Fatalf("stacktrace %q does not name the handler", st[:min(len(st), 200)])
			}
		})
	}
}

// TestTriage_Aborted covers structured-logging / Scenario: Aborted request.
func TestTriage_Aborted(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		h      srvr.HandlerFunc
		status any
	}{
		{"before writing", func(http.ResponseWriter, *http.Request) error { panic(http.ErrAbortHandler) }, nil},
		{"after a partial body", func(w http.ResponseWriter, _ *http.Request) error {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("part"))
			http.NewResponseController(w).Flush()
			panic(http.ErrAbortHandler)
		}, float64(200)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			base, out := loggedServer(t, func(g *srvr.Group) { g.GET("/x", tt.h) })
			req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/x", nil)
			if res, err := clientFor(req.URL.String()).Do(req); err == nil {
				_, rerr := io.ReadAll(res.Body)
				res.Body.Close()
				if tt.status == nil || rerr == nil {
					t.Fatalf("got a complete response %d, want an aborted connection", res.StatusCode)
				}
			}

			rec := out.waitRecord(t, "http.request.failed")
			_, hasProblem := rec["problem"]
			if rec["level"] != "WARN" || rec["error.kind"] != "panic" || rec["error.type"] != "panic" ||
				rec["srvr.response.aborted"] != true || rec["http.response.status_code"] != tt.status || hasProblem {
				t.Fatalf("record = %v", rec)
			}
		})
	}
}

var hex16 = regexp.MustCompile(`^[0-9a-f]{16}$`)

// TestTriage_Fingerprint covers triage-error-records / Scenario: Fingerprint stability.
func TestTriage_Fingerprint(t *testing.T) {
	t.Parallel()

	// fingerprints starts a server of its own for the calling subtest, so
	// the subtests run in parallel, and returns a function that sends one
	// request and returns the fingerprint of the failed record it adds.
	fingerprints := func(t *testing.T) func(method, path string) string {
		t.Helper()

		base, out := loggedServer(t, func(g *srvr.Group) {
			g.GET("/users/{id}", func(_ http.ResponseWriter, r *http.Request) error {
				switch r.URL.Query().Get("e") {
				case "once":
					return fmt.Errorf("user %s: %w", r.PathValue("id"), sql.ErrNoRows)
				case "twice":
					return fmt.Errorf("b: %w", fmt.Errorf("a: %w", sql.ErrNoRows))
				case "panic":
					panic(r.URL.Query().Get("v"))
				default:
					return errors.New("a")
				}
			})
			g.POST("/users", returning(errors.New("a")))
			// A route for every method: Mount handlers return no error, so the
			// "Mount handler" rows use a method-less srvr route.
			g.Handle("", "/files", returning(errors.New("a")))
			g.Handle("QUERY", "/search", returning(errors.New("a")))
			g.POST("/search", returning(errors.New("a")))
		})

		return func(method, path string) string {
			t.Helper()
			before := len(out.find(t, "http.request.failed"))
			do(t, method, base+path, nil, nil)
			var recs []map[string]any
			eventually(t, func() bool { recs = out.find(t, "http.request.failed"); return len(recs) > before }, "no record")
			fp, _ := recs[len(recs)-1]["error.fingerprint"].(string)
			if !hex16.MatchString(fp) {
				t.Fatalf("fingerprint %q is not 16 hex characters", fp)
			}

			return fp
		}
	}

	pairs := []struct {
		name          string
		first, second [2]string
		equal         bool
	}{
		{"same type chain, other message", [2]string{"GET", "/users/1?e=once"}, [2]string{"GET", "/users/2?e=once"}, true},
		{"wrapped once vs twice", [2]string{"GET", "/users/1?e=once"}, [2]string{"GET", "/users/1?e=twice"}, false},
		{"other route and method", [2]string{"GET", "/users/1"}, [2]string{"POST", "/users"}, false},
		{"panics with other values", [2]string{"GET", "/users/1?e=panic&v=x"}, [2]string{"GET", "/users/1?e=panic&v=y"}, true},
		// client-chosen methods
		{"two unknown methods, unmatched", [2]string{"FOO1", "/nope"}, [2]string{"FOO2", "/nope"}, true},
		{"unknown vs GET, unmatched", [2]string{"FOO1", "/nope"}, [2]string{"GET", "/nope"}, false},
		{"two unknown methods on a route for every method", [2]string{"PURGE", "/files"}, [2]string{"BREW", "/files"}, true},
		{"QUERY vs POST", [2]string{"QUERY", "/search"}, [2]string{"POST", "/search"}, false},
		{"lowercase get vs GET, unmatched", [2]string{"get", "/nope"}, [2]string{"GET", "/nope"}, false},
	}

	for _, p := range pairs {
		t.Run(p.name, func(t *testing.T) {
			t.Parallel()

			fingerprint := fingerprints(t)
			a, b := fingerprint(p.first[0], p.first[1]), fingerprint(p.second[0], p.second[1])
			if (a == b) != p.equal {
				t.Errorf("%s vs %s, want equal %v", a, b, p.equal)
			}
		})
	}
}

type (
	selfError      struct{}
	badError       struct{}
	badUnwrapError struct{}
	MyError        struct{}
	deepError      struct{ next error }
)

func (selfError) Error() string      { return "self" }
func (e selfError) Unwrap() error    { return e }
func (badError) Error() string       { panic("error") }
func (badUnwrapError) Error() string { return "bad unwrap" }
func (badUnwrapError) Unwrap() error { panic("unwrap") }
func (*MyError) Error() string       { return "my" }
func (e deepError) Error() string    { return "deep" }
func (e deepError) Unwrap() error    { return e.next }

// TestTriage_Hostile covers triage-error-records / Scenario: Hostile error values.
func TestTriage_Hostile(t *testing.T) {
	t.Parallel()

	var deep error
	deep = errors.New("bottom")
	for range 99 {
		deep = deepError{deep}
	}
	many := make([]error, 100)
	for i := range many {
		many[i] = errors.New("e")
	}
	var typedNil *MyError

	tests := []struct {
		name  string
		err   error
		check func(rec map[string]any) bool
	}{
		{"100 levels", deep, func(r map[string]any) bool { return len(chainOf(r)) == 32 && r["srvr.error.truncated"] == true }},
		{"self unwrap", selfError{}, func(r map[string]any) bool { return len(chainOf(r)) <= 32 && r["srvr.error.truncated"] == true }},
		{"join of 100", errors.Join(many...), func(r map[string]any) bool { return len(chainOf(r)) == 64 && r["srvr.error.truncated"] == true }},
		{"panicking Error", badError{}, func(r map[string]any) bool { return r["exception.message"] == "<panic in Error(): srvr_test.badError>" }},
		{"panicking Unwrap", fmt.Errorf("w: %w", badUnwrapError{}), func(r map[string]any) bool {
			return strings.Join(chainOf(r), ",") == "*fmt.wrapError,srvr_test.badUnwrapError" && r["srvr.error.truncated"] == true
		}},
		{"typed nil", typedNil, func(r map[string]any) bool {
			return r["error.type"] == "*srvr_test.MyError" && r["exception.message"] == "<nil *srvr_test.MyError>"
		}},
		{"1 MiB message", errors.New(xs(1 << 20)), func(r map[string]any) bool { return r["exception.message"] == xs(8192)+cut }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			base, out := loggedServer(t, func(g *srvr.Group) { g.GET("/x", returning(tt.err)) })
			if got := get(t, base+"/x").status; got != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", got)
			}
			if rec := out.waitRecord(t, "http.request.failed"); !tt.check(rec) {
				t.Fatalf("record = %.600v", rec)
			}
		})
	}
}

// TestTriage_Build covers triage-error-records / Scenario: Build
// attributes on errors, with build information injected through
// srvr.WithBuildInfo (export_test.go).
func TestTriage_Build(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		revision string // empty: no vcs.revision in the build information
	}{
		{"revision 9c1e2ab", "9c1e2ab"},
		{"no revision", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			build := func() (*debug.BuildInfo, bool) {
				info := &debug.BuildInfo{Main: debug.Module{Version: "v9.9.9"}}
				if tt.revision != "" {
					info.Settings = []debug.BuildSetting{{Key: "vcs.revision", Value: tt.revision}}
				}

				return info, true
			}

			rec := failedRecord(t, returning(errors.New("boom")),
				srvr.WithServiceConfig(srvr.ServiceConfig{Name: "registration-api", Version: "1.4.2"}),
				srvr.WithBuildInfo(build))
			if rec["service.name"] != "registration-api" || rec["service.version"] != "1.4.2" {
				t.Fatalf("record = %v, want service registration-api 1.4.2", rec)
			}
			got, has := rec["vcs.revision"]
			if tt.revision == "" && has {
				t.Fatalf("vcs.revision = %v, want none", got)
			}
			if tt.revision != "" && got != tt.revision {
				t.Fatalf("vcs.revision = %v, want %s", got, tt.revision)
			}
		})
	}
}

// TestTriage_Redactor covers triage-error-records / Scenario: Redacting error text.
func TestTriage_Redactor(t *testing.T) {
	t.Parallel()

	emailMasker := func(s string) string { return regexp.MustCompile(`[^ ]+@`).ReplaceAllString(s, "***@") }
	panicky := func(string) string { panic("redactor") }

	tests := []struct {
		name    string
		opts    []srvr.Option
		message string
	}{
		{"none", nil, "duplicate key alice@example.com"},
		{"email masker", []srvr.Option{srvr.WithErrorRedactor(emailMasker)}, "duplicate key ***@example.com"},
		{"panicking", []srvr.Option{srvr.WithErrorRedactor(panicky)}, "[redaction failed]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := failedRecord(t, returning(errors.New("duplicate key alice@example.com")), tt.opts...)["exception.message"]; got != tt.message {
				t.Fatalf("exception.message = %v, want %q", got, tt.message)
			}
		})
	}

	t.Run("nil", func(t *testing.T) {
		t.Parallel()

		if _, err := srvr.New(srvr.WithErrorRedactor(nil)); !errors.Is(err, srvr.ErrConfig) {
			t.Fatalf("New = %v, want ErrConfig", err)
		}
	})
}

func recurse(n int) {
	if n <= 1 {
		panic("deep")
	}
	recurse(n - 1)
}

// TestTriage_PanicStackSize covers the recursion rows of
// triage-error-records / Scenario: Oversized stack.
func TestTriage_PanicStackSize(t *testing.T) {
	t.Parallel()

	for _, depth := range []int{1, 10000} {
		t.Run(strconv.Itoa(depth), func(t *testing.T) {
			t.Parallel()

			rec := failedRecord(t, func(http.ResponseWriter, *http.Request) error { recurse(depth); return nil })
			if st := fmt.Sprint(rec["exception.stacktrace"]); len(st) > 32768 || rec["srvr.stacktrace.truncated"] != false {
				t.Fatalf("stacktrace %d bytes, truncated %v", len(st), rec["srvr.stacktrace.truncated"])
			}
		})
	}
}

// TestDefaultRedaction covers triage-error-records / Scenario: Default
// redaction.
func TestDefaultRedaction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		opts          []srvr.Option
		text, message string
	}{
		// masked by default

		{
			"URL query values", nil,
			`charge: Get "http://pay.local/v1/charge?api_key=SECRET&amount=5": dial tcp: refused`,
			`charge: Get "http://pay.local/v1/charge?api_key=***&amount=***": dial tcp: refused`,
		},
		{"password pair", nil, "login failed: password=hunter2 user=bob", "login failed: password=*** user=bob"},
		{"authorization header", nil, "upstream said Authorization: Bearer abc.def", "upstream said Authorization: ***"},
		{"session cookie", nil, "bad cookie session_id=7f3a; Path=/", "bad cookie session_id=***; Path=/"},
		{"upper-case token key", nil, "ACCESS_TOKEN=xyz rejected", "ACCESS_TOKEN=*** rejected"},
		{"e-mail stays", nil, "duplicate key alice@example.com", "duplicate key alice@example.com"},
		{"path stays", nil, "GET http://a.local/users/42 failed", "GET http://a.local/users/42 failed"},
		{"odd query stays", nil, "query ?=x and ?& are odd", "query ?=x and ?& are odd"},
		{"empty", nil, "", ""},
		// overrides
		{"WithErrorRedactor", []srvr.Option{srvr.WithErrorRedactor(strings.ToUpper)}, "password=hunter2", "PASSWORD=HUNTER2"},
		{
			"WithoutErrorRedaction",
			[]srvr.Option{srvr.WithoutErrorRedaction()},
			"login failed: password=hunter2", "login failed: password=hunter2",
		},
		{
			"WithoutErrorRedaction, then WithErrorRedactor",
			[]srvr.Option{srvr.WithoutErrorRedaction(), srvr.WithErrorRedactor(strings.ToUpper)},
			"password=hunter2", "PASSWORD=HUNTER2",
		},
		{
			"WithErrorRedactor, then WithoutErrorRedaction",
			[]srvr.Option{srvr.WithErrorRedactor(strings.ToUpper), srvr.WithoutErrorRedaction()},
			"password=hunter2", "password=hunter2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := failedRecord(t, returning(errors.New(tt.text)), tt.opts...)["exception.message"]; got != tt.message {
				t.Fatalf("exception.message = %v, want %q", got, tt.message)
			}
		})
	}

	// The first row's text, from a real client error value.
	t.Run("URL query values in a client error", func(t *testing.T) {
		t.Parallel()

		err := &url.Error{Op: "Get", URL: "http://pay.local/v1/charge?api_key=SECRET", Err: errors.New("dial tcp: refused")}
		const want = `charge: Get "http://pay.local/v1/charge?api_key=***": dial tcp: refused`
		if got := failedRecord(t, returning(fmt.Errorf("charge: %w", err)))["exception.message"]; got != want {
			t.Fatalf("exception.message = %v, want %q", got, want)
		}
	})
}

// TestDefaultRedaction_Lifecycle covers triage-error-records / Scenario:
// Default redaction of panics and lifecycle text.
func TestDefaultRedaction_Lifecycle(t *testing.T) {
	t.Parallel()

	panicking := func(v any) srvr.HandlerFunc {
		return func(http.ResponseWriter, *http.Request) error { panic(v) }
	}

	tests := []struct {
		name              string
		record            func(t *testing.T) map[string]any
		attribute         string
		contained, absent string
	}{
		{
			name:      "handler panic message",
			record:    func(t *testing.T) map[string]any { t.Helper(); return failedRecord(t, panicking("token=abc")) },
			attribute: "exception.message", contained: "token=***", absent: "token=abc",
		},
		{
			name: "handler panic stack",
			record: func(t *testing.T) map[string]any {
				t.Helper()
				return failedRecord(t, panicking(errors.New("password=p")))
			},
			// a Go stack does not quote the panic value, so only absence is checked
			attribute: "exception.stacktrace", absent: "password=p",
		},
		{
			name: "BeforeDrain hook error",
			record: func(t *testing.T) map[string]any {
				t.Helper()
				out := &logBuffer{}
				srv := newServer(t, srvr.WithLogOutput(out), srvr.WithShutdownHook(srvr.BeforeDrain, func(context.Context) error {
					return errors.New("secret=s")
				}))
				mustNil(t, srv.Register(okRoute("/x")))
				done := runBackground(t, srv)
				_ = srv.Shutdown(t.Context())
				if err := receive(t, done, "Start did not return"); !errors.Is(err, srvr.ErrShutdownHook) {
					t.Fatalf("Start = %v, want an error wrapping ErrShutdownHook", err)
				}

				return out.waitRecord(t, "server.shutdown_hook.failed")
			},
			attribute: "exception.message", contained: "secret=***", absent: "secret=s",
		},
		{
			name: "configurer panic",
			record: func(t *testing.T) map[string]any {
				t.Helper()
				out := &logBuffer{}
				srv := newServer(t, srvr.WithLogOutput(out))
				mustNil(t, srv.Register(configFunc(func(*srvr.Group) { panic("api_key=k") })))
				if err := srv.Start(t.Context()); err == nil {
					t.Fatal("Start = nil, want an error")
				}

				return out.waitRecord(t, "server.failed")
			},
			attribute: "exception.message", contained: "api_key=***", absent: "api_key=k",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, _ := tt.record(t)[tt.attribute].(string)
			if strings.Contains(got, tt.absent) {
				t.Fatalf("%s contains %q: %s", tt.attribute, tt.absent, got)
			}
			if tt.contained != "" && !strings.Contains(got, tt.contained) {
				t.Fatalf("%s = %q, want it to contain %q", tt.attribute, got, tt.contained)
			}
		})
	}
}
