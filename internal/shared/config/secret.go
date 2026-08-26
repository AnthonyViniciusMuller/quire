package config

import "log/slog"

// redacted replaces a secret wherever it would otherwise be rendered.
const redacted = "[REDACTED]"

// Secret is a configuration value that must never reach a log line, an error
// message or a stack dump. Formatting it yields [redacted]; the underlying
// value is available only through [Secret.Reveal], which makes every use of it
// visible at the call site.
type Secret string

// String implements [fmt.Stringer] and hides the value.
func (s Secret) String() string { return redacted }

// LogValue implements [slog.LogValuer] and hides the value.
func (s Secret) LogValue() slog.Value { return slog.StringValue(redacted) }

// MarshalText implements [encoding.TextMarshaler] and hides the value, so that
// a secret cannot leak through a JSON or YAML dump of the configuration.
func (s Secret) MarshalText() ([]byte, error) { return []byte(redacted), nil }

// Reveal returns the value in the clear. Call it only where the secret is
// actually consumed, never to pass it along.
func (s Secret) Reveal() string { return string(s) }

// IsZero reports whether the secret is empty.
func (s Secret) IsZero() bool { return s == "" }
