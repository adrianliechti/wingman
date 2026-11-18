package config

import (
	"strings"
	"testing"
)

func TestAutoRouterConfiguration(t *testing.T) {
	const routes = `
routers:
  auto:
    type: auto
    routes:
      - {model: pool, when: everyday}
      - {model: gpt-6-sol, when: complex}
  pool:
    type: adaptive
    models: [gpt-6-luna]
`
	cfg, err := parseTestConfig(t, testDecisionProviders+routes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.Completer("auto"); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ field, replacement, want string }{
		{"type: auto", "type: auto\n    decider: missing", "decision model not found"},
		{"model: pool", "model: missing", "completer not found"},
		{"model: pool", "model: gpt-6-sol", "duplicate auto route"},
		{"when: everyday", "when: ''", "when description"},
		{"type: auto", "type: auto\n    models: [pool]", "uses routes"},
		{"type: auto", "type: classifier", "router"},
	} {
		_, err := parseTestConfig(t, testDecisionProviders+strings.Replace(routes, tc.field, tc.replacement, 1))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("configuration with %q: got %v, want %s", tc.replacement, err, tc.want)
		}
	}
}
