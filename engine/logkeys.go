package engine

// The log schema: every event name and attribute key srvr writes. It is a
// public, versioned commitment (see LOGGING.md): adding a key keeps
// schemaVersion; renaming or removing one, or changing its meaning or value
// type, bumps it. TestLogSchemaGolden pins it.
const (
	// schemaVersion is the value of keySchema on every record srvr writes.
	schemaVersion = 1

	eventRequestCompleted = "http.request.completed"
	eventRequestFailed    = "http.request.failed"
	eventStarted          = "server.started"
	eventStopping         = "server.stopping"
	eventStopped          = "server.stopped"
	eventFailed           = "server.failed"
	eventHookFailed       = "server.shutdown_hook.failed"

	keyServerAddress  = "server.address"
	keyDrainDuration  = "srvr.drain.duration_ms"
	keyDrainForced    = "srvr.drain.forced"
	keyHookPhase      = "srvr.shutdown_hook.phase"
	keyHookIndex      = "srvr.shutdown_hook.index"
	keyFunction       = "code.function.name"
	keyExceptionMsg   = "exception.message"
	keyServiceName    = "service.name"
	keyServiceVersion = "service.version"
	keyVCSRevision    = "vcs.revision"
	keyErrorType      = "error.type"
	keyErrorKind      = "error.kind"
	keyErrorChain     = "error.chain"
	keyErrorTruncated = "srvr.error.truncated"
	keyFingerprint    = "error.fingerprint"
	keyExceptionType  = "exception.type"
	keyStacktrace     = "exception.stacktrace"
	keyStackTruncated = "srvr.stacktrace.truncated"
	keyAborted        = "srvr.response.aborted"
	keyFilePath       = "code.file.path"
	keyLineNumber     = "code.line.number"
	keyEnvironment    = "deployment.environment.name"

	keySchema        = "srvr.log.schema"
	keyTraceID       = "trace_id"
	keySpanID        = "span_id"
	keyMethod        = "http.request.method"
	keyRoute         = "http.route"
	keyStatus        = "http.response.status_code"
	keyBodySize      = "http.response.body.size"
	keyDuration      = "duration_ms"
	keyUserAgent     = "user_agent.original"
	keyAttrTruncated = "srvr.attr.truncated"
	keyCommitted     = "srvr.response.committed"
	keyURLPath       = "url.path"
	keyURLQuery      = "url.query"
	keyClientAddress = "client.address"
	keyProblem       = "problem"
	keyProblemStatus = "status"
	keyProblemType   = "type"
	keyProblemTitle  = "title"
)
