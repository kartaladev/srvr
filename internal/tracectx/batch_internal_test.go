package tracectx

import (
	"bytes"
	"testing"
)

// TestIDBatch covers design D7 (b): a batch hands out each byte once,
// clears what it handed out, so it holds only future ids, and refills from
// crypto/rand when it is used up.
func TestIDBatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		draws    int
		wantLeft int
	}{
		{"first draw fills an empty batch", 1, (batchTraces - 1) * rawLen},
		{"last draw of a batch", batchTraces, 0},
		{"draw past the batch refills it", batchTraces + 1, (batchTraces - 1) * rawLen},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var b idBatch
			seen := map[[rawLen]byte]bool{}

			var raw [rawLen]byte
			for range tt.draws {
				b.next(&raw)

				if seen[raw] || raw == [rawLen]byte{} {
					t.Fatalf("draw %x repeats an earlier draw or is all zeros", raw)
				}

				seen[raw] = true
			}

			if b.left != tt.wantLeft {
				t.Fatalf("left = %d, want %d", b.left, tt.wantLeft)
			}

			if handedOut := b.buf[b.left:]; !bytes.Equal(handedOut, make([]byte, len(handedOut))) {
				t.Fatalf("handed-out bytes are kept: %x", handedOut)
			}
		})
	}
}
