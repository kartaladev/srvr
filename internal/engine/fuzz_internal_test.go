package engine

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzCutString covers test-quality / Scenario: Fuzzed properties, row
// FuzzCutString: the result is at most the limit plus the suffix, a
// prefix of the input followed by the suffix when cut and the input itself
// otherwise, and valid UTF-8 whenever the input is.
func FuzzCutString(f *testing.F) {
	for _, seed := range []struct {
		s   string
		max int
	}{
		{"", 0},
		{"abc", 3},
		{"abcd", 3},
		{"é", 1},
		{"aé", 2},
		{"日本語", 4},
		{"x", -1},
		{strings.Repeat("x", 2000), 1024},
		{"\xff\xfe", 1},
		{"àb", 2},
	} {
		f.Add(seed.s, seed.max)
	}

	f.Fuzz(func(t *testing.T, s string, maxBytes int) {
		if maxBytes > 1<<16 {
			maxBytes %= 1 << 16
		}

		got, cut := cutString(s, maxBytes)

		switch {
		case !cut:
			if got != s || exceeds(len(s), maxBytes) {
				t.Fatalf("cutString(%q, %d) = %q, false; want the input, uncut only within the limit", s, maxBytes, got)
			}
		case !strings.HasSuffix(got, truncatedSuffix) || !strings.HasPrefix(s, strings.TrimSuffix(got, truncatedSuffix)):
			t.Fatalf("cutString(%q, %d) = %q, want a prefix of the input and the suffix", s, maxBytes, got)
		case len(got) > maxBytes+len(truncatedSuffix):
			t.Fatalf("cutString(%q, %d) has %d bytes, want at most %d", s, maxBytes, len(got), maxBytes+len(truncatedSuffix))
		}

		if utf8.ValidString(s) && !utf8.ValidString(got) {
			t.Fatalf("cutString(%q, %d) = %q is not valid UTF-8", s, maxBytes, got)
		}
	})
}

// FuzzLogLine covers test-quality / Scenario: Fuzzed properties, row
// FuzzLogLine: logging any string as the message and as an attribute
// through srvr's handler writes exactly one line, which decodes as JSON
// back to the cut value.
func FuzzLogLine(f *testing.F) {
	for _, seed := range []string{
		"", "hello", "line\nbreak", "\r\n\t", `"quoted" \ back`, "\x00\x1b[31m", "  ",
		"日本語", "\xff invalid", strings.Repeat("é", 600), `{"msg":"forged"}`,
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, s string) {
		var buf bytes.Buffer

		h := newLogHandler(&LogSettings{Output: &buf})

		slog.New(h).Info(s, "value", s)

		out := buf.Bytes()
		if bytes.Count(out, []byte("\n")) != 1 || !bytes.HasSuffix(out, []byte("\n")) {
			t.Fatalf("logging %q wrote %q, want exactly one line", s, out)
		}

		var rec map[string]any
		if err := json.Unmarshal(out, &rec); err != nil {
			t.Fatalf("logging %q wrote %q, not JSON: %v", s, out, err)
		}

		// Invalid UTF-8 is written with replacement characters, so only a
		// valid string decodes to exactly its cut value.
		if !utf8.ValidString(s) {
			return
		}

		want, _ := cutString(s, defaultMaxAttrBytes)
		if rec["msg"] != want || rec["value"] != want {
			t.Fatalf("logging %q decoded msg %q value %q, want %q", s, rec["msg"], rec["value"], want)
		}
	})
}

// FuzzRedactSecrets: the keyword pre-scan never changes what RedactSecrets
// returns, which equals both patterns applied without it, including for
// text whose non-ASCII runes (?i) folds onto the key words.
func FuzzRedactSecrets(f *testing.F) {
	for _, seed := range []string{
		"",
		"password=hunter2",
		"Key=v",
		"ſession: abc",
		"Get \"https://x.test/?api_key=abc&page=2\": timeout",
		"TOKEN:\"x\"",
		"no secret at all",
		"pass word=x",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, text string) {
		want := secretPair.ReplaceAllString(queryValue.ReplaceAllString(text, "${1}"+redactedValue), "${1}"+redactedValue)
		if got := RedactSecrets(text); got != want {
			t.Fatalf("RedactSecrets(%q) = %q, want %q", text, got, want)
		}
	})
}
