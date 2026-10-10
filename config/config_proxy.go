package config

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/adrianliechti/wingman/pkg/otel"
	"github.com/adrianliechti/wingman/pkg/provider"
)

type proxyConfig struct {
	URL string `yaml:"url"`
}

func (cfg *proxyConfig) proxyTransport() (*http.Transport, error) {
	if cfg == nil || cfg.URL == "" {
		return nil, nil
	}

	proxyURL, err := url.Parse(cfg.URL)

	if err != nil {
		return nil, err
	}

	if proxyURL.Hostname() == "" || (proxyURL.Scheme != "http" && proxyURL.Scheme != "https" && proxyURL.Scheme != "socks5" && proxyURL.Scheme != "socks5h") {
		return nil, errors.New("proxy URL must be an absolute http, https, socks5 or socks5h URL")
	}

	tr := provider.DefaultTransport()
	tr.Proxy = http.ProxyURL(proxyURL)

	return tr, nil
}

func (cfg *proxyConfig) proxyClient() (*http.Client, error) {
	transport, err := cfg.proxyTransport()

	if err != nil {
		return nil, err
	}

	return &http.Client{
		Transport: otel.Transport(transport),
	}, nil
}
