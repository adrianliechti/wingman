package config

import (
	"crypto/tls"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/adrianliechti/wingman/pkg/limiter"
	"github.com/adrianliechti/wingman/pkg/otel"
	"github.com/adrianliechti/wingman/pkg/scraper"
	"github.com/adrianliechti/wingman/pkg/scraper/custom"
	"github.com/adrianliechti/wingman/pkg/scraper/exa"
	"github.com/adrianliechti/wingman/pkg/scraper/jina"
	"github.com/adrianliechti/wingman/pkg/scraper/tavily"
	"golang.org/x/time/rate"
)

func (cfg *Config) RegisterScraper(id string, p scraper.Provider) {
	if cfg.scraper == nil {
		cfg.scraper = make(map[string]scraper.Provider)
	}

	if _, ok := cfg.scraper[""]; !ok {
		cfg.scraper[""] = p
	}

	cfg.scraper[id] = p
}

func (cfg *Config) Scraper(id string) (scraper.Provider, error) {
	if cfg.scraper != nil {
		if c, ok := cfg.scraper[id]; ok {
			return c, nil
		}
	}

	return nil, errors.New("scraper not found: " + id)
}

type scraperConfig struct {
	Type string `yaml:"type"`

	URL   string `yaml:"url"`
	Token string `yaml:"token"`

	Vars  map[string]string `yaml:"vars"`
	Proxy *proxyConfig      `yaml:"proxy"`

	Limit *int `yaml:"limit"`
}

type scraperContext struct {
	Limiter *rate.Limiter
}

func (cfg *Config) registerScrapers(f *configFile) error {
	var configs map[string]scraperConfig

	if err := f.Scrapers.Decode(&configs); err != nil {
		return err
	}

	for _, node := range f.Scrapers.Content {
		id := node.Value

		config, ok := configs[node.Value]

		if !ok {
			continue
		}

		context := scraperContext{
			Limiter: createLimiter(config.Limit),
		}

		scraper, err := createScraper(config, context)

		if err != nil {
			return err
		}

		if _, ok := scraper.(limiter.Scraper); !ok {
			scraper = limiter.NewScraper(context.Limiter, scraper)
		}

		if _, ok := scraper.(otel.Scraper); !ok {
			scraper = otel.NewScraper(id, "", scraper)
		}

		cfg.RegisterScraper(id, scraper)
	}

	return nil
}

func createScraper(cfg scraperConfig, context scraperContext) (scraper.Provider, error) {
	switch strings.ToLower(cfg.Type) {

	case "exa":
		return exaScraper(cfg)

	case "jina":
		return jinaScraper(cfg)

	case "tavily":
		return tavilyScraper(cfg)

	case "custom", "wingman-scraper":
		return customScraper(cfg)

	default:
		return nil, errors.New("invalid scraper type: " + cfg.Type)
	}
}

func exaScraper(cfg scraperConfig) (scraper.Provider, error) {
	var options []exa.Option

	if cfg.Proxy != nil && cfg.Proxy.URL != "" {
		proxyURL, err := url.Parse(cfg.Proxy.URL)

		if err != nil {
			return nil, err
		}

		client := &http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyURL(proxyURL),

				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true,
				},
			},
		}

		options = append(options, exa.WithClient(client))
	}

	return exa.New(cfg.Token, options...)
}

func jinaScraper(cfg scraperConfig) (scraper.Provider, error) {
	var options []jina.Option

	if cfg.Token != "" {
		options = append(options, jina.WithToken(cfg.Token))
	}

	return jina.New(cfg.URL, options...)
}

func tavilyScraper(cfg scraperConfig) (scraper.Provider, error) {
	return tavily.New(cfg.Token)
}

func customScraper(cfg scraperConfig) (scraper.Provider, error) {
	var options []custom.Option

	return custom.New(cfg.URL, options...)
}
