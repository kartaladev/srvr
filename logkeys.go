package srvr

// The log schema: every event name and attribute key srvr writes. It is a
// public, versioned commitment (see LOGGING.md): adding a key keeps
// schemaVersion; renaming or removing one, or changing its meaning or value
// type, bumps it. TestLogSchemaGolden pins it.
const (
	// schemaVersion is the value of keySchema on every record srvr writes.
	schemaVersion = 1

	eventRequestCompleted = "http.request.completed"
	eventRequestFailed    = "http.request.failed"

	keySchema        = "srvr.log.schema"
	keyTraceID       = "trace_id"
	keySpanID        = "span_id"
	keyMethod        = "http.request.method"
	keyRoute         = "http.route"
	keyStatus        = "http.response.status_code"
	keyBodySize      = "http.response.body.size"
	keyDuration      = "duration_ms"
	keyUserAgent     = "user_agent.original"
	keyCommitted     = "srvr.response.committed"
	keyProblem       = "problem"
	keyProblemStatus = "status"
	keyProblemType   = "type"
	keyProblemTitle  = "title"
)
