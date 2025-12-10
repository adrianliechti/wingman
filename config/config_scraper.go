package config

import (
	"errors"
	"net/http"
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
	Client  *http.Client
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

		if config.Proxy != nil {
			client, err := config.Proxy.proxyClient()

			if err != nil {
				return err
			}

			context.Client = client
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
		return exaScraper(cfg, context)

	case "jina":
		return jinaScraper(cfg, context)

	case "tavily":
		return tavilyScraper(cfg, context)

	case "custom", "wingman-scraper":
		return customScraper(cfg, context)

	default:
		return nil, errors.New("invalid scraper type: " + cfg.Type)
	}
}

func exaScraper(cfg scraperConfig, context scraperContext) (scraper.Provider, error) {
	var options []exa.Option

	if context.Client != nil {
		options = append(options, exa.WithClient(context.Client))
	}

	return exa.New(cfg.Token, options...)
}

func jinaScraper(cfg scraperConfig, context scraperContext) (scraper.Provider, error) {
	var options []jina.Option

	if cfg.Token != "" {
		options = append(options, jina.WithToken(cfg.Token))
	}

	if context.Client != nil {
		options = append(options, jina.WithClient(context.Client))
	}

	return jina.New(cfg.URL, options...)
}

func tavilyScraper(cfg scraperConfig, context scraperContext) (scraper.Provider, error) {
	var options []tavily.Option

	if context.Client != nil {
		options = append(options, tavily.WithClient(context.Client))
	}

	return tavily.New(cfg.Token, options...)
}

func customScraper(cfg scraperConfig, context scraperContext) (scraper.Provider, error) {
	var options []custom.Option

	return custom.New(cfg.URL, options...)
}
