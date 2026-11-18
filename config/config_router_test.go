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
		{"type: auto", "type: classifier", "routes and decider require router type auto"},
		{"type: adaptive", "type: classifier", "invalid router type: classifier"},
	} {
		_, err := parseTestConfig(t, testDecisionProviders+strings.Replace(routes, tc.field, tc.replacement, 1))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("configuration with %q: got %v, want %s", tc.replacement, err, tc.want)
		}
	}
}

func TestRouterReferencesResolveInAnyOrder(t *testing.T) {
	const routers = `
routers:
  pool:
    type: roundrobin
    models: [gpt-6-sol]
    fallback: outer
  outer:
    type: auto
    routes:
      - {model: inner, when: everyday}
      - {model: gpt-6-sol, when: complex}
  inner:
    type: auto
    routes:
      - {model: gpt-6-luna, when: everyday}
      - {model: gpt-6-sol, when: complex}
`
	cfg, err := parseTestConfig(t, testDecisionProviders+routers)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"pool", "outer", "inner"} {
		if _, err := cfg.Completer(id); err != nil {
			t.Error(err)
		}
	}

	// A model may be wrapped by a router of the same name.
	if _, err := parseTestConfig(t, testDecisionProviders+"\nrouters:\n  gpt-6-sol:\n    type: roundrobin\n    models: [gpt-6-sol]\n"); err != nil {
		t.Error(err)
	}

	_, err = parseTestConfig(t, testDecisionProviders+strings.Replace(routers, "model: gpt-6-luna", "model: outer", 1))
	if err == nil || !strings.Contains(err.Error(), "routers reference each other: inner, outer") {
		t.Errorf("circular routers: got %v", err)
	}
}
