package codex

type Config struct {
	command string
	model   string
}

type Option func(*Config)

// WithCommand overrides the path to the `codex` binary. Default: "codex",
// invoked as `codex app-server` (stdio transport).
func WithCommand(command string) Option {
	return func(c *Config) {
		c.command = command
	}
}
