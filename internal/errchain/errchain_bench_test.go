package errchain

import (
	"strings"
	"testing"
)

// BenchmarkRedactSecrets measures the default redactor over representative
// error and stack texts.
func BenchmarkRedactSecrets(b *testing.B) {
	frame := "github.com/example/app/internal/store.(*Users).Find(0x14000123450, {0x1045a2f40, 0x14000234560})\n" +
		"\t/home/build/app/internal/store/users.go:42 +0x1c4\n"

	cases := []struct {
		name string
		text string
	}{
		{"no secret", "store: find user 42: connection refused"},
		{"URL query secret", `Get "https://api.example.com/v1/users?api_key=abc123&page=2": dial tcp: i/o timeout`},
		{"key=value secret", "auth: login failed for user=alice password=hunter2 retry=3"},
		{"32 KiB stack", strings.Repeat(frame, 32<<10/len(frame))},
	}

	for _, bc := range cases {
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_ = RedactSecrets(bc.text)
			}
		})
	}
}
