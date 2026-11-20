// Package engine holds the pieces every srvr engine shares: the
// configuration model and its validation, the lifecycle state machine, the
// per-request state, trace context, the access and triage record builders,
// the log handler and the guards against faulty consumer code.
//
// This package is internal to srvr's own modules: the net/http engine in
// package srvr, the ginsrvr and fibersrvr engine modules, otelsrvr and
// otelsetup. Go's internal rule is by import path, so those nested modules
// import it and a module outside github.com/kartaladev/srvr cannot.
// Applications use package srvr and the engine modules. [Settings] is
// written only by srvr's option functions.
//
// The configuration types and sentinel errors are declared here and
// re-exported by package srvr under the same names, so srvr.Config and
// engine.Config are one type and errors.Is matches either sentinel.
package engine
