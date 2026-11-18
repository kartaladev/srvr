package otelsetup

import "time"

// FlushTimeout returns the effective flush timeout of s.
func FlushTimeout(s *Setup) time.Duration { return s.cfg.FlushTimeout }
