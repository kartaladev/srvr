package otelsrvr

// EffectiveConfig returns the configuration o was built with, defaults
// applied.
func EffectiveConfig(o *Observer) Config { return o.config }
