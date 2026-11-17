package fibersrvr_test

import (
	"context"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// TestFiberShutdownNeverServed pins the fiber fact design D3 relies on:
// ShutdownWithContext on an app that never served returns nil (fiber's
// ErrNotRunning is only for an app without a fasthttp server, which
// fiber.New always creates).
func TestFiberShutdownNeverServed(t *testing.T) {
	t.Parallel()

	if err := fiber.New().ShutdownWithContext(context.Background()); err != nil {
		t.Fatalf("ShutdownWithContext = %v, want nil", err)
	}
}
