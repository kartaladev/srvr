// Package gincycle holds the parity test for gin's cyclic c.Error chain
// (test files only). It is a test binary of its own because the handler
// goroutine that test leaves stuck cannot be stopped: only this binary
// ignores it, and it spins on no other test's CPU.
package gincycle
