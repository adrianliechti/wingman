package config

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/extractor"
)

const testProviderConfig = `
providers:
  - type: openai
    token: test-key
    models:
      test-model:
        type: completer
        id: gpt-test
`

func TestConfigRegistrationUsesMappingKeysOnce(t *testing.T) {
	var tokens []string
	previous := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: googleTranslatorRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		tokens = append(tokens, req.Header.Get("Authorization"))
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"results":[{"flagged":false}]}`))}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = previous })
	cfg, err := parseTestConfig(t, `
guards:
  "":
    type: openai
    url: https://guard.test/v1
    token: first
  second:
    type: openai
    url: https://guard.test/v1
    token: second
`)
	if err != nil {
		t.Fatal(err)
	}
	guard, err := cfg.Guard("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := guard.Check(context.Background(), "input", nil); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(tokens, []string{"Bearer first", "Bearer second"}) {
		t.Fatalf("guard calls = %v", tokens)
	}
}

func TestConfigSectionAliasesAndDefaultModels(t *testing.T) {
	cfg, err := parseTestConfig(t, testProviderConfig+`
extractors: &llm_providers
  first:
    type: llm
summarizers: *llm_providers
translators: *llm_providers
agents:
  assistant:
    type: assistant
  react:
    type: react
`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.Extractor("first"); err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.Summarizer("first"); err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.Translator("first"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"assistant", "react"} {
		if _, err := cfg.Completer(id); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConfigRejectsMissingProviderReferences(t *testing.T) {
	for _, tt := range []struct{ name, config, want string }{
		{"extractor model", "extractors:\n  test: {type: llm, model: missing}\n", "completer not found: missing"},
		{"translator model", "translators:\n  test: {type: llm, model: missing}\n", "completer not found: missing"},
		{"summarizer model", "summarizers:\n  test: {type: llm, model: missing}\n", "completer not found: missing"},
		{"agent model", "agents:\n  test: {type: react, model: missing}\n", "completer not found: missing"},
		{"researcher model", "researchers:\n  test: {type: agent, model: missing}\n", "completer not found: missing"},
		{"researcher scraper", "searchers:\n  search: {type: duckduckgo}\nresearchers:\n  test: {type: agent, scraper: missing}\n", "scraper not found: missing"},
		{"researcher searcher", "researchers:\n  test: {type: agent, searcher: missing}\n", "searcher not found: missing"},
		{"tool scraper", "tools:\n  test: {type: scraper, scraper: missing}\n", "scraper not found: missing"},
		{"tool searcher", "tools:\n  test: {type: search, searcher: missing}\n", "searcher not found: missing"},
		{"tool translator", "tools:\n  test: {type: translator, translator: missing}\n", "translator not found: missing"},
		{"tool researcher", "tools:\n  test: {type: research, researcher: missing}\n", "researcher not found: missing"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseTestConfig(t, testProviderConfig+tt.config)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %s", err, tt.want)
			}
		})
	}
}

func TestResearcherUsesDefaultSearcher(t *testing.T) {
	cfg, err := parseTestConfig(t, testProviderConfig+`
searchers:
  search: {type: duckduckgo}
researchers:
  research: {type: agent}
`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.Researcher("research"); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPExtractorClientConfiguration(t *testing.T) {
	for _, kind := range []string{"azure", "docling", "kreuzberg", "mistral"} {
		t.Run(kind, func(t *testing.T) {
			called := false
			sentinel := errors.New("configured HTTP client")
			client := &http.Client{Transport: googleTranslatorRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				called = true
				if kind == "mistral" && req.URL.String() != "https://api.mistral.ai/v1/ocr" {
					t.Errorf("Mistral endpoint = %s", req.URL)
				}
				return nil, sentinel
			})}
			p, err := createExtractor(extractorConfig{Type: kind, URL: "https://extractor.test", Token: "key"}, extractorContext{Client: client})
			if err != nil {
				t.Fatal(err)
			}
			_, err = p.Extract(context.Background(), extractor.File{Name: "report.pdf"}, nil)
			if !called || !errors.Is(err, sentinel) {
				t.Fatalf("client called = %v, error = %v", called, err)
			}
		})
	}
}

func TestExtractorMistralModelConfiguration(t *testing.T) {
	previous := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: googleTranslatorRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		data, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), `"model":"configured-ocr-model"`) {
			t.Errorf("OCR model not configured: %s", data)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"pages":[]}`))}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = previous })
	cfg, err := parseTestConfig(t, "extractors:\n  ocr: {type: mistral, model: configured-ocr-model}\n")
	if err != nil {
		t.Fatal(err)
	}
	p, err := cfg.Extractor("ocr")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Extract(context.Background(), extractor.File{Name: "report.pdf"}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestConfigRejectsMalformedProxyURLs(t *testing.T) {
	for _, family := range []string{"extractors", "tools", "mcps"} {
		t.Run(family, func(t *testing.T) {
			kind := map[string]string{"extractors": "docling", "tools": "mcp", "mcps": "proxy"}[family]
			_, err := parseTestConfig(t, family+":\n  test:\n    type: "+kind+"\n    url: https://service.test\n    proxy: {url: relative-proxy}\n")
			if err == nil || !strings.Contains(err.Error(), "proxy URL") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestConfigRejectsEmptyCustomTargets(t *testing.T) {
	for _, family := range []string{"extractors", "translators", "searchers", "scrapers", "guards", "segmenters", "summarizers", "researchers", "tools"} {
		t.Run(family, func(t *testing.T) {
			_, err := parseTestConfig(t, family+":\n  test: {type: custom, url: 'grpc://'}\n")
			if err == nil {
				t.Fatal("accepted an empty gRPC target")
			}
		})
	}
}

func TestConfigRejectsMalformedHTTPExtractorEndpoints(t *testing.T) {
	for _, kind := range []string{"azure", "docling", "kreuzberg"} {
		t.Run(kind, func(t *testing.T) {
			_, err := parseTestConfig(t, "extractors:\n  test: {type: "+kind+", url: relative-endpoint}\n")
			if err == nil {
				t.Fatal("accepted a relative HTTP endpoint")
			}
		})
	}
}

func TestMCPProxyConfigurationUsesTransport(t *testing.T) {
	called := false
	transport := googleTranslatorRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		called = true
		if req.URL.String() != "https://mcp.test/mcp" || req.Header.Get("X-API-Key") != "key" {
			t.Errorf("request = %s, headers %v", req.URL, req.Header)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("response"))}, nil
	})
	p, err := createMCP(mcpConfig{Type: "proxy", URL: "https://mcp.test/mcp", Vars: map[string]string{"X-API-Key": "key"}}, mcpContext{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	p.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", nil))
	if !called || w.Code != http.StatusOK || w.Body.String() != "response" {
		t.Fatalf("transport called = %v, response = %+v", called, w)
	}
}

func TestConfigRejectsUnusedToolFields(t *testing.T) {
	for _, field := range []string{"model", "extractor"} {
		t.Run(field, func(t *testing.T) {
			_, err := parseTestConfig(t, "tools:\n  test: {type: search, "+field+": unused}\n")
			if err == nil || !strings.Contains(err.Error(), field) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func parseTestConfig(t *testing.T, contents string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return Parse(path)
}
