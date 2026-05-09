package claude

type Config struct {
	command string
	model   string
}

type Option func(*Config)

// WithCommand overrides the path to the `claude` binary. Default is to look
// up "claude" on PATH.
func WithCommand(command string) Option {
	return func(c *Config) {
		c.command = command
	}
}
