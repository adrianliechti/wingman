package config

import "testing"

func TestCodingRouterExample(t *testing.T) {
	cfg, err := Parse("../examples/coding-router.yaml")
	if err != nil {
		t.Fatal(err)
	}

	for _, model := range []string{"coding", "coding-stage"} {
		if _, err := cfg.Completer(model); err != nil {
			t.Errorf("example does not register %q: %v", model, err)
		}
	}
}
