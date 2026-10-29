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
	cfg.Budget = budget.NewAsync(p, 256, 4)
	return nil
}
