package azurespeech

import (
	"net/http"
)

type Config struct {
	region string

	token string
	model string

	client *http.Client
}

type Option func(*Config)

func WithClient(client *http.Client) Option {
	return func(c *Config) {
		c.client = client
	}
}

func WithToken(token string) Option {
	return func(c *Config) {
		c.token = token
	}
}

func (c *Config) ttsURL() string {
	return "https://" + c.region + ".tts.speech.microsoft.com"
}

func (c *Config) sttURL() string {
	return "https://" + c.region + ".api.cognitive.microsoft.com"
}
