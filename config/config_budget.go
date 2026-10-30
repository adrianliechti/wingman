package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/budget"
	"github.com/adrianliechti/wingman/pkg/budget/custom"
)

type budgetConfig struct {
	Type    string `yaml:"type"`
	URL     string `yaml:"url"`
	Token   string `yaml:"token"`
	Timeout string `yaml:"timeout"`

	// Queue bounds buffered reports; Workers deliver them concurrently.
	Queue   int `yaml:"queue"`
	Workers int `yaml:"workers"`
}

func (cfg *Config) registerBudget(f *configFile) error {
	if f.Budget == nil {
		return nil
	}
	b := f.Budget
	if !strings.EqualFold(b.Type, "custom") {
		return fmt.Errorf("invalid budget type: %s", b.Type)
	}
	options := []custom.Option{custom.WithToken(b.Token)}
	if b.Timeout != "" {
		timeout, err := time.ParseDuration(b.Timeout)
		if err != nil || timeout <= 0 {
			return fmt.Errorf("invalid budget timeout: %q", b.Timeout)
		}
		options = append(options, custom.WithTimeout(timeout))
	}
	p, err := custom.New(b.URL, options...)
	if err != nil {
		return err
	}
	queue, workers := b.Queue, b.Workers
	if queue == 0 {
		queue = 1024
	}
	if workers == 0 {
		workers = 8
	}
	if queue < 0 || workers < 0 {
		p.Close()
		return fmt.Errorf("invalid budget queue %d or workers %d", b.Queue, b.Workers)
	}
	cfg.Budget = budget.NewAsync(p, queue, workers)
	return nil
}
