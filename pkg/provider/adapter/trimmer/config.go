package trimmer

type Config struct {
	tokenThreshold int
	keepTurns      int
	keepToolUses   int
	charsPerToken  int
}

type Option func(*Config)

func WithTokenThreshold(threshold int) Option {
	return func(c *Config) {
		c.tokenThreshold = threshold
	}
}

func WithKeepTurns(turns int) Option {
	return func(c *Config) {
		c.keepTurns = turns
	}
}

func WithKeepToolUses(uses int) Option {
	return func(c *Config) {
		c.keepToolUses = uses
	}
}

func WithCharsPerToken(chars int) Option {
	return func(c *Config) {
		c.charsPerToken = chars
	}
}
