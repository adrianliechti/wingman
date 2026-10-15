package config

import (
	"errors"
	"net/http"
	"strings"

	"github.com/adrianliechti/wingman/pkg/guard"
	"github.com/adrianliechti/wingman/pkg/middleware"
	"github.com/adrianliechti/wingman/pkg/otel"
	"github.com/adrianliechti/wingman/pkg/scraper"
	"github.com/adrianliechti/wingman/pkg/scraper/custom"
	"github.com/adrianliechti/wingman/pkg/scraper/exa"
	"github.com/adrianliechti/wingman/pkg/scraper/fetch"
	"github.com/adrianliechti/wingman/pkg/scraper/tavily"
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

	Guard string `yaml:"guard"`

	Vars  map[string]string `yaml:"vars"`
	Proxy *proxyConfig      `yaml:"proxy"`
}

type scraperContext struct {
	Client *http.Client
}

func (cfg *Config) registerScrapers(f *configFile) error {
	var configs map[string]scraperConfig

	if err := decodeStrict(&f.Scrapers, &configs); err != nil {
		return err
	}

	for _, id := range configIDs(&f.Scrapers) {

		config, ok := configs[id]

		if !ok {
			continue
		}

		context := scraperContext{}

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

		scraper.Use(otel.ScraperMiddleware(config.Type, id))

		if config.Guard != "" {
			g, err := cfg.Guard(config.Guard)

			if err != nil {
				return err
			}

			scraper.Use(guard.ScraperMiddleware(g))
		}

		cfg.RegisterScraper(id, scraper.Build())
	}

	return nil
}

func createScraper(cfg scraperConfig, context scraperContext) (*middleware.Builder[scraper.Provider], error) {
	var p scraper.Provider
	var err error

	switch strings.ToLower(cfg.Type) {

	case "fetch":
		p, err = fetchScraper(cfg, context)

	case "exa":
		p, err = exaScraper(cfg, context)

	case "tavily":
		p, err = tavilyScraper(cfg, context)

	case "custom", "wingman-scraper":
		p, err = customScraper(cfg, context)

	default:
		return nil, errors.New("invalid scraper type: " + cfg.Type)
	}

	if err != nil {
		return nil, err
	}

	return middleware.NewBuilder(p), nil
}

func exaScraper(cfg scraperConfig, context scraperContext) (scraper.Provider, error) {
	var options []exa.Option

	if context.Client != nil {
		options = append(options, exa.WithClient(context.Client))
	}

	return exa.New(cfg.Token, options...)
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

func fetchScraper(cfg scraperConfig, context scraperContext) (scraper.Provider, error) {
	var options []fetch.Option

	if context.Client != nil {
		options = append(options, fetch.WithClient(context.Client))
	}

	return fetch.New(options...)
}
