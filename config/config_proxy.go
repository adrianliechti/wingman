package config

import (
	"crypto/tls"
	"net/http"
	"net/url"
)

type proxyConfig struct {
	URL string `yaml:"url"`
}

func (cfg *proxyConfig) proxyTransport() (*http.Transport, error) {
	if cfg == nil || cfg.URL == "" {
		return nil, nil
	}

	url, err := url.Parse(cfg.URL)

	if err != nil {
		return nil, err
	}

	return &http.Transport{
		Proxy: http.ProxyURL(url),

		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}, nil
}

func (cfg *proxyConfig) proxyClient() (*http.Client, error) {
	transport, err := cfg.proxyTransport()

	if err != nil {
		return nil, err
	}

	return &http.Client{
		Transport: transport,
	}, nil
}
