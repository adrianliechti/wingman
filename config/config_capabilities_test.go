package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCapabilitiesOverridesAreRejected(t *testing.T) {
	for _, family := range []string{"extractors", "translators"} {
		for _, provider := range []string{"custom", "llm"} {
			t.Run(family+"/"+provider, func(t *testing.T) {
				contents := family + ":\n  remote:\n    type: " + provider + "\n    url: grpc://localhost:9000\n    capabilities: {}\n"
				filename := filepath.Join(t.TempDir(), "config.yaml")
				if err := os.WriteFile(filename, []byte(contents), 0o600); err != nil {
					t.Fatal(err)
				}
				if _, err := Parse(filename); err == nil || !strings.Contains(err.Error(), "capabilities") {
					t.Fatalf("configuration error = %v, want rejected capabilities field", err)
				}
			})
		}
	}
}
