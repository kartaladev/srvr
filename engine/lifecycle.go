package engine

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Runner serves the routes of one started server on a listener: the
// engine-specific part of the lifecycle.
type Runner interface {
	// Serve serves on ln until Drain is called or serving fails. It returns
	// the reason serving stopped; after Drain began, its result is ignored.
	Serve(ln net.Listener) error
	// Drain stops serving and waits for in-flight requests until ctx is
	// done, then closes what is left and returns ctx's error.
	Drain(ctx context.Context) error
}

// state is the lifecycle state of a server. It only moves forward:
// new -> starting -> running -> stopping -> closed. A server that stops
// without serving goes from new or starting to stopping (Shutdown) or
// closed (a failed Start).
type state int

const (
	stateNew state = iota
	stateStarting
	stateRunning
	stateStopping
	stateClosed
)

// startable returns nil when Start may begin from this state.
func (st state) startable() error {
	switch st {
	case stateNew:
		return nil
	case stateStarting, stateRunning:
		return ErrServerStarted
	default:
		return ErrServerClosed
	}
}

// Lifecycle is the single-use state machine every engine's server runs
// on: Start configures, binds and serves through a [Runner]; Shutdown and
// the end of Start's context stop it in order (BeforeDrain hooks, drain,
// AfterDrain hooks). It holds its lock only to read and move state, so
// consumer code it calls may call the server's own methods. Its methods
// are safe for concurrent use.
//
// C is the engine's configurer type: the lifecycle keeps the configurers
// registered while the server is new and hands them to the configure phase.
type Lifecycle[C any] struct {
	core *Core
	// configurers are appended under mu while the server is new and read
	// by Start after it left that state.
	configurers []C
	// ready is closed once the listener is bound.
	ready chan struct{}

	mu   sync.Mutex
	st   state
	addr string
	// interrupt cancels, with cause ErrServerClosed, the context the
	// starting steps (route checks, binding) receive; set while starting.
	interrupt context.CancelCauseFunc
	// run is the serving part of a running server, nil before.
	run *run
	// stopped is closed when the server reaches stateClosed. hookErr and
	// drainErr are the results of its stop sequence: Start returns hookErr,
	// the Shutdown call that stopped the server returns both.
	stopped  chan struct{}
	hookErr  error
	drainErr error
}

// NewLifecycle returns the lifecycle of a new server configured by c.
func NewLifecycle[C any](c *Core) *Lifecycle[C] {
	return &Lifecycle[C]{core: c, ready: make(chan struct{}), stopped: make(chan struct{})}
}

// Register keeps conf for the configure phase while the server is new,
// and returns [ErrServerStarted] or [ErrServerClosed] otherwise. A nil or
// typed-nil conf returns an error wrapping [ErrRouteConfig] in any state.
func (l *Lifecycle[C]) Register(conf C) error {
	if IsNil(conf) {
		return RouteError("configurer %T is nil", conf)
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.st.startable(); err != nil {
		return err
	}

	l.configurers = append(l.configurers, conf)

	return nil
}

// Ready returns a channel closed once the listener is bound and the server
// accepts connections. It is never closed when Start fails.
func (l *Lifecycle[C]) Ready() <-chan struct{} { return l.ready }

// Addr returns the bound address once the server is ready, and the empty
// string before.
func (l *Lifecycle[C]) Addr() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.addr
}

// Start runs prepare with the registered configurers (the configure
// phase, which no later Register can change), runs the route checks on the
// listing it returns, binds the listener, closes Ready and serves through the
// returned Runner until ctx is done or Shutdown is called; then it stops
// the server in order. See srvr.Server.Start for the contract every
// engine shares.
func (l *Lifecycle[C]) Start(ctx context.Context, prepare func(configurers []C) (Runner, Routes, error)) error {
	if ctx == nil {
		return ConfigError("Start context", "<nil>", "is nil")
	}

	// stepCtx is what the starting steps receive: Shutdown cancels it.
	// Waiting on a step that ignores it still ends only with ctx.
	stepCtx, interrupt := context.WithCancelCause(ctx)
	defer interrupt(nil)

	if err := l.begin(interrupt); err != nil {
		return err
	}

	if err := ctx.Err(); err != nil {
		return l.fail(ctx, err)
	}

	runner, routes, err := prepare(l.configurers)
	if err == nil {
		err = l.core.checkRoutes(ctx, stepCtx, routes) // runs no check once interrupted
	}

	var ln net.Listener
	if err == nil && !interrupted(stepCtx) { // nothing is bound once interrupted
		ln, err = l.listen(stepCtx)
	}

	switch {
	case interrupted(stepCtx):
		return l.closeInterrupted(ctx, ln)
	case err != nil:
		return l.fail(ctx, err)
	}

	addr, err := listenerAddr(ln)
	if err != nil {
		return l.fail(ctx, ConfigError("Listener", TypeName(ln), err.Error()))
	}

	r := l.serve(ctx, ln, addr, runner)
	if r == nil {
		// Shutdown was called after the last interruption check.
		return l.closeInterrupted(ctx, ln)
	}

	var served error

	select {
	case <-ctx.Done():
	case <-r.stopping:
	case <-r.done:
		if served = r.servedErr(r.err); served != nil {
			// Serving failed on its own; the server still stops in order.
			l.logFailed(ctx, served)
		}
	}

	drainCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), l.core.cfg.ShutdownTimeout)
	defer cancel()

	l.stop(drainCtx)
	<-l.stopped

	return errors.Join(served, l.hookErr)
}

// begin moves a new server to starting, keeping interrupt for Shutdown.
func (l *Lifecycle[C]) begin(interrupt context.CancelCauseFunc) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.st.startable(); err != nil {
		return err
	}

	l.st = stateStarting
	l.interrupt = interrupt

	return nil
}

// interrupted reports whether Shutdown interrupted the starting steps that
// receive stepCtx.
func interrupted(stepCtx context.Context) bool {
	return errors.Is(context.Cause(stepCtx), ErrServerClosed)
}

// closeInterrupted closes a server whose start Shutdown interrupted:
// nothing was served, so only the AfterDrain hooks run, and Start returns
// ErrServerClosed with their failures. A listener Start already bound (ln;
// nil when none) is closed; a WithListener listener is closed either way.
// It is not a failure, so no server.failed record is written.
func (l *Lifecycle[C]) closeInterrupted(ctx context.Context, ln net.Listener) error {
	if ln == nil {
		ln = l.core.settings.Listener
	}

	l.closeUnserved(ctx, ln)

	return errors.Join(ErrServerClosed, l.hookErr)
}

// fail ends a Start that failed before serving: nothing was served, so
// only the AfterDrain hooks run, and err is returned with their failures.
func (l *Lifecycle[C]) fail(ctx context.Context, err error) error {
	l.logFailed(ctx, err)
	l.closeUnserved(ctx, l.core.settings.Listener)

	return errors.Join(err, l.hookErr)
}

// logFailed writes server.failed for err, the error Start returns.
func (l *Lifecycle[C]) logFailed(ctx context.Context, err error) {
	l.core.logEvent(ctx, slog.LevelError, eventFailed, l.core.exceptionMessage(errorText(err)))
}

// closeUnserved closes a server that served nothing: it closes ln, the
// listener the server owns (nil when there is none), and runs only the
// AfterDrain hooks.
func (l *Lifecycle[C]) closeUnserved(ctx context.Context, ln net.Listener) {
	if ln != nil {
		closeListener(ln)
	}

	l.finish(l.core.runAfterDrain(ctx), nil)
}

// finish closes the server with the results of its stop sequence.
func (l *Lifecycle[C]) finish(hookErr, drainErr error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.st = stateClosed
	l.hookErr, l.drainErr = hookErr, drainErr
	close(l.stopped)
}

// listen returns the configured listener or binds the configured address.
func (l *Lifecycle[C]) listen(ctx context.Context) (net.Listener, error) {
	if ln := l.core.settings.Listener; ln != nil {
		return ln, nil
	}

	return new(net.ListenConfig).Listen(ctx, "tcp", l.core.cfg.Addr)
}

// listenerAddr returns the text of ln's address. A nil or typed-nil
// address, or a panic in Addr or String, is an error: ln is consumer code,
// and its address is read without trusting it.
func listenerAddr(ln net.Listener) (addr string, err error) {
	defer func() {
		if v := recover(); v != nil {
			addr, err = "", errors.New("reading its address panicked: "+ValueText(v))
		}
	}()

	switch a := ln.Addr(); {
	case a == nil:
		return "", errors.New("its address is nil")
	case IsNil(a):
		return "", errors.New("its address is a nil " + TypeName(a))
	default:
		return a.String(), nil
	}
}

// run is the serving part of a running server.
type run struct {
	runner Runner
	ln     net.Listener
	// err is the result of Runner.Serve; read it only after done closed.
	err error
	// done is closed when serveOn has returned.
	done chan struct{}
	// stopping is closed when the stop sequence begins.
	stopping chan struct{}
}

// servedErr returns err, the result of Serve, or nil when Serve returned
// because the stop sequence closed the listener.
func (r *run) servedErr(err error) error {
	select {
	case <-r.stopping:
		return nil
	default:
		return err
	}
}

// serve moves the server to running and serves on ln, whose address is
// addr. It returns nil, leaving ln and the server to Start, when Shutdown
// was called while starting. It calls no consumer code under l.mu.
func (l *Lifecycle[C]) serve(ctx context.Context, ln net.Listener, addr string, runner Runner) *run {
	l.mu.Lock()

	if l.st == stateStopping {
		l.mu.Unlock()

		return nil
	}

	r := &run{
		runner:   runner,
		ln:       &guardedListener{Listener: ln},
		done:     make(chan struct{}),
		stopping: make(chan struct{}),
	}
	l.run = r
	l.st = stateRunning
	l.addr = addr
	l.mu.Unlock()

	go l.serveOn(r)

	l.core.logEvent(ctx, slog.LevelInfo, eventStarted, slog.String(keyServerAddress, addr))
	close(l.ready)

	return r
}

// serveOn serves r on its listener until the runner's Serve returns,
// keeping its result in r.err and then closing r.done. It is a named
// method so a goroutine leak report names it.
func (l *Lifecycle[C]) serveOn(r *run) {
	defer close(r.done)

	r.err = r.runner.Serve(r.ln)
}

// serveWait bounds how long the stop sequence waits for serveOn to return
// after the listener was closed: a consumer listener whose Close does not
// unblock Accept would otherwise hang the stop.
const serveWait = time.Second

// joinServe waits until r's serveOn returned, for at most serveWait, after
// which it is abandoned.
func joinServe(r *run) {
	t := time.NewTimer(serveWait)
	defer t.Stop()

	select {
	case <-r.done:
	case <-t.C:
	}
}

// stop runs the stop sequence of a running server, unless it has already
// begun; a caller that finds it begun can wait on l.stopped.
func (l *Lifecycle[C]) stop(drainCtx context.Context) {
	l.mu.Lock()
	if l.st != stateRunning {
		l.mu.Unlock()

		return
	}

	l.st = stateStopping
	r := l.run
	l.mu.Unlock()

	l.stopRun(drainCtx, r)
}

// stopRun is the stop sequence of a running server: stop accepting
// connections, BeforeDrain hooks, drain, AfterDrain hooks.
func (l *Lifecycle[C]) stopRun(drainCtx context.Context, r *run) {
	close(r.stopping)
	_ = r.ln.Close()
	l.core.logEvent(drainCtx, slog.LevelInfo, eventStopping)

	begin := time.Now()
	beforeErr := l.core.runHooks(drainCtx, BeforeDrain)
	drainErr := r.runner.Drain(drainCtx)
	l.core.logStopped(drainCtx, time.Since(begin), drainErr)
	afterErr := l.core.runAfterDrain(drainCtx)
	joinServe(r)

	l.finish(errors.Join(beforeErr, afterErr), drainErr)
}

// Shutdown stops the server gracefully; see srvr.Server.Shutdown for the
// contract every engine shares.
func (l *Lifecycle[C]) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return ConfigError("Shutdown context", "<nil>", "is nil")
	}

	l.mu.Lock()
	st := l.st

	switch st {
	case stateNew:
		// Stopping, so a concurrent Start or Register is refused while the
		// AfterDrain hooks run.
		l.st = stateStopping
		l.mu.Unlock()
		l.closeUnserved(ctx, l.core.settings.Listener)

		return l.hookErr
	case stateStarting:
		// Start's starting steps are interrupted, or Start sees the stopping
		// state before it serves; either way Start closes the server, and
		// this call reports the result.
		l.st = stateStopping
		interrupt := l.interrupt
		l.mu.Unlock()
		interrupt(ErrServerClosed)

		return l.wait(ctx, true)
	case stateRunning:
		// Claimed under the lock that read the state, so no other stop can
		// begin in between.
		l.st = stateStopping
		r := l.run
		l.mu.Unlock()

		drainCtx, cancel := context.WithTimeout(ctx, l.core.cfg.ShutdownTimeout)
		defer cancel()

		l.stopRun(drainCtx, r)

		return l.result()
	default:
		l.mu.Unlock()

		return l.wait(ctx, false)
	}
}

// wait waits until the server is closed or ctx is done. It returns the
// stop result when first, nil otherwise.
func (l *Lifecycle[C]) wait(ctx context.Context, first bool) error {
	select {
	case <-l.stopped:
		if first {
			return l.result()
		}

		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// result is what the Shutdown call that stopped the server returns: the
// hook failures and the drain error. Read it only after l.stopped closed.
func (l *Lifecycle[C]) result() error { return errors.Join(l.hookErr, l.drainErr) }

// guardedListener is the listener the server serves on. Its Close runs the
// consumer's Close once and returns nil to every caller, the first
// included: srvr ignores Close errors and panics on every path, and
// http.Server.Shutdown, which closes the listeners it still tracks and
// returns that error, would otherwise report a forced drain depending on
// whether Serve had already returned. The Once runs the consumer's Close
// under its own lock, which guards no srvr state: a second caller waits
// only for the same Close it would have called itself.
//
// Close and Accept run the consumer's methods under recover. Serve calls
// Accept on a goroutine of its own, where a panic would end the process:
// the panic becomes an error that is not a net.Error, so Serve returns it
// and Start stops the server. Nothing else is added per accepted
// connection.
type guardedListener struct {
	net.Listener

	once sync.Once
}

func (l *guardedListener) Accept() (conn net.Conn, err error) {
	defer func() {
		if v := recover(); v != nil {
			conn, err = nil, ConfigError("Listener", TypeName(l.Listener), "Accept panicked: "+ValueText(v))
		}
	}()

	return l.Listener.Accept()
}

func (l *guardedListener) Close() error {
	l.once.Do(func() { closeListener(l.Listener) })

	return nil
}

// closeListener closes ln, ignoring an error or a panic from its Close, so
// the stop sequence still completes.
func closeListener(ln net.Listener) {
	defer func() { _ = recover() }()

	_ = ln.Close()
}

// logStopped writes server.stopped: INFO, or WARN with srvr.drain.forced
// when the drain deadline passed and connections were closed.
func (c *Core) logStopped(ctx context.Context, d time.Duration, drainErr error) {
	attrs := []slog.Attr{slog.Float64(keyDrainDuration, millis(d))}
	level := slog.LevelInfo

	if drainErr != nil {
		level = slog.LevelWarn
		attrs = append(attrs, slog.Bool(keyDrainForced, true))
	}

	c.logEvent(ctx, level, eventStopped, attrs...)
}

// runAfterDrain runs the AfterDrain hooks within their own budget.
func (c *Core) runAfterDrain(parent context.Context) error {
	ctx, cancel := c.afterDrainContext(parent)
	defer cancel()

	return c.runHooks(ctx, AfterDrain)
}

// HTTPRunner returns the Runner of an engine served by net/http: an
// http.Server for h with the limits of c, which [Core.Config] has
// completed.
func HTTPRunner(c Config, h http.Handler) Runner {
	return &httpRunner{hs: c.httpServer(h), served: make(chan struct{})}
}

type httpRunner struct {
	hs *http.Server
	// served is closed when Serve has returned.
	served chan struct{}
}

func (r *httpRunner) Serve(ln net.Listener) error {
	defer close(r.served)

	return r.hs.Serve(ln)
}

// Drain stops accepting connections and waits for in-flight requests until
// ctx is done, then closes the remaining connections. Any Shutdown error
// counts as a forced drain: the listener's Close never returns one
// (guardedListener), so only ctx's error is left.
//
// http.Server.Shutdown and Close first wait, without regard to ctx, for
// Serve to return; a consumer listener whose Close does not unblock Accept
// would hold them forever. So when ctx ends while Serve is still running,
// Drain returns at once and the close finishes in the background once
// Accept returns.
func (r *httpRunner) Drain(ctx context.Context) error {
	shut := make(chan error, 1)
	go func() { shut <- r.hs.Shutdown(ctx) }()

	var err error
	select {
	case err = <-shut:
	case <-ctx.Done():
		select {
		case <-r.served:
			// Shutdown no longer waits for Serve: it sees ctx at once.
			err = <-shut
		default:
			go func() {
				<-shut //nolint:errcheck // ctx's error, already returned below
				_ = r.hs.Close()
			}()

			return ctx.Err()
		}
	}

	if err != nil {
		_ = r.hs.Close()
	}

	return err
}

// logEvent writes one of srvr's own records, outside any request:
// schema version, the event's attributes, then the service attributes.
func (c *Core) logEvent(ctx context.Context, level slog.Level, event string, attrs ...slog.Attr) {
	if !c.log.Enabled(ctx, level) {
		return
	}

	r := newOwnRecord(level, event)
	r.AddAttrs(attrs...)
	r.AddAttrs(c.serviceAttrs...)
	c.log.handle(ctx, r, true)
}

// Warn writes server.config.warning for a configuration that works but is
// likely unintended: code is one of the declared [WarningCode] values and
// message tells the consumer what to change. An engine
// calls it once per warning, at construction for engine settings or at
// Start for route-dependent ones; a warning never fails either.
func (c *Core) Warn(ctx context.Context, code WarningCode, message string) {
	c.logEvent(ctx, slog.LevelWarn, eventConfigWarning, slog.String(keyWarningCode, string(code)), slog.String(keyWarningMessage, message))
}

// newOwnRecord starts one of srvr's own records: no source (PC 0) and the
// schema version first.
func newOwnRecord(level slog.Level, event string) slog.Record {
	r := slog.NewRecord(time.Now(), level, event, 0)
	r.AddAttrs(slog.Int(keySchema, schemaVersion))

	return r
}

// millis returns d in milliseconds, for the *_ms keys of the log schema.
func millis(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// Location is a function's code location, for code.function.name,
// code.file.path and code.line.number.
type Location struct {
	function string
	file     string
	line     int
}

// Function returns the qualified function name, or "" when unknown.
func (l Location) Function() string { return l.function }

// FuncPointer returns the code pointer of fn, a function value, as a key
// for tables built once per route: the same function gives the same key.
func FuncPointer(fn any) uintptr {
	v := reflect.ValueOf(fn)
	if v.Kind() != reflect.Func {
		return 0
	}

	return v.Pointer()
}

// FuncLocation returns the Location of fn. A method value resolves to its
// "-fm" wrapper, whose suffix is removed; an unknown PC yields the zero
// Location, never an invented one.
func FuncLocation(fn any) Location {
	v := reflect.ValueOf(fn)
	if v.Kind() != reflect.Func || v.IsNil() {
		return Location{}
	}

	f := runtime.FuncForPC(v.Pointer())
	if f == nil {
		return Location{}
	}

	loc := Location{function: strings.TrimSuffix(f.Name(), "-fm")}

	// A method value's wrapper is generated code: its file and line would
	// point nowhere, so they are omitted rather than invented.
	if file, line := f.FileLine(f.Entry()); file != "<autogenerated>" {
		loc.file, loc.line = file, line
	}

	return loc
}

// HandlerLocation returns the Location of a plain handler: its type's
// ServeHTTP method, or the function of an http.HandlerFunc.
func HandlerLocation(h http.Handler) Location {
	if hf, ok := h.(http.HandlerFunc); ok {
		return FuncLocation(hf)
	}

	var loc Location
	if m, ok := reflect.TypeOf(h).MethodByName("ServeHTTP"); ok {
		loc = FuncLocation(m.Func.Interface())
	}

	loc.function = TypeName(h) + ".ServeHTTP"

	return loc
}

// ShutdownPhase is when a shutdown hook runs while the server stops.
// Consumers use it as srvr.ShutdownPhase.
type ShutdownPhase int

const (
	// BeforeDrain hooks run after the listener is closed and before
	// in-flight requests are drained, within the drain budget
	// (ShutdownTimeout), for example to signal open sessions to finish.
	BeforeDrain ShutdownPhase = iota + 1
	// AfterDrain hooks run after the drain, with their own budget
	// (ShutdownHookTimeout), for example to flush telemetry or close a
	// database pool. They also run when Start fails after leaving its
	// initial state and when Shutdown closes a server that never started.
	AfterDrain
)

// String returns the phase name.
func (p ShutdownPhase) String() string {
	switch p {
	case BeforeDrain:
		return "BeforeDrain"
	case AfterDrain:
		return "AfterDrain"
	default:
		return "ShutdownPhase(" + strconv.Itoa(int(p)) + ")"
	}
}

// defaultShutdownHookTimeout gives AfterDrain hooks time to flush without
// starving on a slow drain: 15s drain plus 5s fits a 30s grace period.
const defaultShutdownHookTimeout = 5 * time.Second

// Hook is a registered shutdown hook: srvr.WithShutdownHook appends one
// to [Settings].
type Hook struct {
	Phase ShutdownPhase
	Fn    func(context.Context) error
}

// runHooks runs the hooks of phase in order, each within ctx, and returns
// their failures, each wrapping ErrShutdownHook.
func (c *Core) runHooks(ctx context.Context, phase ShutdownPhase) error {
	var errs []error

	for i, h := range c.settings.Hooks {
		if h.Phase != phase {
			continue
		}

		if err := runHook(ctx, h.Fn); err != nil {
			c.logEvent(ctx, slog.LevelWarn, eventHookFailed,
				slog.String(keyHookPhase, phase.String()),
				slog.Int(keyHookIndex, i),
				slog.String(keyFunction, FuncLocation(h.Fn).Function()),
				c.exceptionMessage(errorText(err)),
			)
			errs = append(errs, fmt.Errorf("%w: %s hook %d: %w", ErrShutdownHook, phase, i, guard(err)))
		}
	}

	return errors.Join(errs...)
}

// runHook runs fn with runGuarded; a hook that outlives ctx fails as
// abandoned.
func runHook(ctx context.Context, fn func(context.Context) error) error {
	err, abandoned := runGuarded(ctx, fn)
	if abandoned {
		// For BeforeDrain hooks ctx.Err() comes from the consumer's Shutdown
		// context.
		return fmt.Errorf("abandoned: %w", guard(ctx.Err()))
	}

	return err
}

// panicError is the failure of consumer code that panicked.
type panicError struct{ value string }

func (e *panicError) Error() string { return "panic: " + e.value }

// goexitValue is the panic value reported for consumer code that ended its
// goroutine with runtime.Goexit (t.FailNow in a test, for example).
const goexitValue = "runtime.Goexit"

// runGuarded runs consumer code fn in its own goroutine, turning a panic
// into a *panicError, and stops waiting when ctx is done: abandoned is
// true then, unless fn finished as ctx ended, which still counts. fn
// ending its goroutine with runtime.Goexit is reported like a panic, so
// no caller waits for a result that would never come.
func runGuarded(ctx context.Context, fn func(context.Context) error) (err error, abandoned bool) { //nolint:revive // the error is fn's result, not a failure of runGuarded.
	result := make(chan error, 1)

	go func() {
		// Left as is only when fn ends the goroutine with runtime.Goexit.
		var err error = &panicError{value: goexitValue}

		defer func() {
			if v := recover(); v != nil {
				err = &panicError{value: ValueText(v)}
			}
			result <- err
		}()

		err = fn(ctx)
	}()

	select {
	case err := <-result:
		return err, false
	case <-ctx.Done():
		select {
		case err := <-result:
			return err, false
		default:
			return nil, true
		}
	}
}

// afterDrainContext returns the context AfterDrain hooks run with: it keeps
// the values of parent but not its cancellation or deadline, so a slow
// drain cannot starve them, and it is bounded by ShutdownHookTimeout
// unless that is disabled. The hooks of the phase share the budget.
func (c *Core) afterDrainContext(parent context.Context) (context.Context, context.CancelFunc) {
	parent = context.WithoutCancel(parent)

	if c.cfg.ShutdownHookTimeout < 0 {
		return context.WithCancel(parent)
	}

	return context.WithTimeout(parent, c.cfg.ShutdownHookTimeout)
}

// ServiceConfig names the service in srvr's lifecycle and error records,
// so a failure maps to the deployed code. Consumers use it as
// srvr.ServiceConfig and pass it with srvr.WithServiceConfig. Defaults follow OpenTelemetry: service.name is
// "unknown_service:<executable>", and service.version and vcs.revision
// come from the binary's build information when present.
type ServiceConfig struct {
	// Name is service.name.
	Name string `json:"name" yaml:"name" env:"NAME"`
	// Version is service.version.
	Version string `json:"version" yaml:"version" env:"VERSION"`
	// Environment is deployment.environment.name, omitted when empty.
	Environment string `json:"environment" yaml:"environment" env:"ENVIRONMENT"`
}

// attrs returns the service and build attributes, reading build
// information with readBuild (debug.ReadBuildInfo outside tests).
func (c ServiceConfig) attrs(readBuild func() (*debug.BuildInfo, bool)) []slog.Attr {
	var version, revision string
	if info, ok := readBuild(); ok && info != nil {
		version = info.Main.Version
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" {
				revision = s.Value
			}
		}
	}

	attrs := []slog.Attr{slog.String(keyServiceName, cmp.Or(c.Name, "unknown_service:"+filepath.Base(os.Args[0])))}
	for _, kv := range [...][2]string{
		{keyServiceVersion, cmp.Or(c.Version, version)},
		{keyVCSRevision, revision},
		{keyEnvironment, c.Environment},
	} {
		if kv[1] != "" {
			attrs = append(attrs, slog.String(kv[0], kv[1]))
		}
	}

	return attrs
}
