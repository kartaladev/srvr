// Package engine holds the pieces every srvr engine shares: the
// configuration model and its validation, the lifecycle state machine, the
// per-request state, trace context, the access and triage record builders,
// and the log handler. srvr's error values and the guards against faulty
// consumer values live in package errchain, which this package builds on.
//
// This package is internal to srvr's own modules: the net/http engine in
// package srvr, the ginsrvr and fibersrvr engine modules, otelsrvr and
// otelsetup. Go's internal rule is by import path, so those nested modules
// import it and a module outside github.com/kartaladev/srvr cannot.
// Applications use package srvr and the engine modules. [Settings] is
// written only by srvr's option functions.
//
// The configuration types (here) and the sentinel errors (errchain) are
// re-exported by package srvr under the same names, so srvr.Config and
// engine.Config are one type and errors.Is matches either sentinel.
package engine
