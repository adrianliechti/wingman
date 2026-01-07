package bedrock

type Config struct {
	model string
	token string
}

type Option func(*Config)

func WithToken(token string) Option {
	return func(c *Config) {
		c.token = token
	}
}
