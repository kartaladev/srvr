// Package engine holds the pieces every srvr engine shares: the
// configuration model and its validation, the lifecycle state machine, the
// per-request state, trace context, the access and triage record builders,
// the log handler and the guards against faulty consumer code.
//
// This package is for srvr's own engine implementations (the net/http
// engine in package srvr, and the ginsrvr and fibersrvr modules). It is
// public only because those engines live in separate Go modules. Its API is
// not stable and may change in any release before v1; applications should
// use package srvr and the engine modules instead. In particular,
// [Settings] is written only by srvr's option functions.
//
// The configuration types and sentinel errors are declared here and
// re-exported by package srvr under the same names, so srvr.Config and
// engine.Config are one type and errors.Is matches either sentinel.
package engine
