package bedrock

type Config struct {
	model string
}

type Option func(*Config)
