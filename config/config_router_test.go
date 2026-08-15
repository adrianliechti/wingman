package config

import (
	"context"
	"iter"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/router/stage"

	"go.yaml.in/yaml/v4"
)

type routerTestCompleter struct{}

func (routerTestCompleter) Complete(context.Context, []provider.Message, *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		yield(&provider.Completion{Message: &provider.Message{
			Role: provider.MessageRoleAssistant, Content: []provider.Content{{Text: "ok"}},
		}}, nil)
	}
}

func TestCreateStage(t *testing.T) {
	cfg := &Config{}
	cfg.RegisterCompleter("capable", routerTestCompleter{})
	cfg.RegisterCompleter("efficient", routerTestCompleter{})

	completer, err := cfg.createStage("coding", routerConfig{
		Capable: "capable", Efficient: "efficient", Picker: "efficient_first",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := completer.(*stage.Completer); !ok {
		t.Fatalf("unexpected completer type %T", completer)
	}
}

func TestDecisionRouterCanReferenceSiblingRouter(t *testing.T) {
	cfg := &Config{}
	cfg.RegisterCompleter("capable", routerTestCompleter{})
	cfg.RegisterCompleter("efficient", routerTestCompleter{})

	var file configFile
	err := yaml.Load([]byte(`
routers:
  efficient-pool:
    type: roundrobin
    models: [efficient]
  coding:
    type: stage
    capable: capable
    efficient: efficient-pool
`), &file, yaml.WithKnownFields())
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.registerRouters(&file); err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.Completer("coding"); err != nil {
		t.Fatal(err)
	}
}
