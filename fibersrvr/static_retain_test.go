package fibersrvr_test

import (
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/kartaladev/srvr/fibersrvr"
)

// keptName is a name an fs.FS received, as kept and as it read then.
type keptName struct {
	kept, received string
}

// nameKeepingFS keeps every name its Open is given, as a consumer file
// system that memoises by name (a map key) may: a Go string is immutable,
// so an fs.FS may keep the names it is given. It also keeps a copy of
// each, to tell later what the name read when Open received it.
type nameKeepingFS struct {
	base  fs.FS
	mu    sync.Mutex
	names []keptName
}

func (f *nameKeepingFS) Open(name string) (fs.File, error) {
	f.mu.Lock()
	f.names = append(f.names, keptName{kept: name, received: strings.Clone(name)})
	f.mu.Unlock()

	return f.base.Open(name)
}

// changed returns the kept names that no longer read as received.
func (f *nameKeepingFS) changed() (changed []keptName, total int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, n := range f.names {
		if n.kept != n.received {
			changed = append(changed, n)
		}
	}

	return changed, len(f.names)
}

// TestStatic_FSNamesOutliveRequest covers static-files / Scenario: Partial
// failure while serving, the fiber row whose consumer fs.FS keeps the
// names Open receives: they must still read as received after their
// requests end. fiber's Ctx.Path is an unsafe view of a pooled buffer,
// which a later request overwrites (slice 5 review, notes.md).
func TestStatic_FSNamesOutliveRequest(t *testing.T) {
	t.Parallel()

	const requests = 200

	tests := []struct {
		name string
		// served says whether the requested files exist.
		served bool
	}{
		{"names of served files", true},
		{"names of missing files", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			site := fstest.MapFS{}
			if tt.served {
				for i := range requests {
					site[fmt.Sprintf("f%03d.js", i)] = &fstest.MapFile{Data: []byte("x")}
				}
			}

			fsys := &nameKeepingFS{base: site}

			srv, _ := newServer(t)
			mustNil(t, srv.Register(fibersrvr.Static("/assets", fsys)))
			base := start(t, srv)

			for i := range requests {
				do(t, http.MethodGet, fmt.Sprintf("%s/assets/f%03d.js", base, i), nil, nil)
			}

			changed, total := fsys.changed()
			if total == 0 {
				t.Fatal("Open never called")
			}

			if len(changed) > 0 {
				t.Errorf("%d of %d names Open received changed after their request, such as %q now reading %q: they alias a reused buffer",
					len(changed), total, changed[0].received, changed[0].kept)
			}
		})
	}
}
