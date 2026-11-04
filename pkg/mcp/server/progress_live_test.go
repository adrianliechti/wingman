package server

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"iter"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/otel"
	"github.com/adrianliechti/wingman/pkg/provider"
	modelopenai "github.com/adrianliechti/wingman/pkg/provider/openai"
	"github.com/adrianliechti/wingman/pkg/researcher"
	"github.com/adrianliechti/wingman/pkg/researcher/agent"
	"github.com/adrianliechti/wingman/pkg/researcher/anthropic"
	"github.com/adrianliechti/wingman/pkg/researcher/exa"
	"github.com/adrianliechti/wingman/pkg/researcher/openai"
	"github.com/adrianliechti/wingman/pkg/researcher/perplexity"
	scrapeexa "github.com/adrianliechti/wingman/pkg/scraper/exa"
	searchexa "github.com/adrianliechti/wingman/pkg/searcher/exa"
	"github.com/adrianliechti/wingman/pkg/tool/research"

	"github.com/joho/godotenv"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type liveResearchUpdate struct {
	Milliseconds int64   `json:"milliseconds"`
	Progress     float64 `json:"progress"`
	Message      string  `json:"message"`
}

// Exercise final response delivery beyond the former keepalive ping timeout.
// No model API is called; this test uses a delayed local researcher over HTTP.
func TestLiveResearchLongRequest(t *testing.T) {
	if os.Getenv("RESEARCH_LIVE") != "1" {
		t.Skip("set RESEARCH_LIVE=1 for long-running HTTP MCP regression")
	}
	s := researchServer(t, func(ctx context.Context, _ string, _ *researcher.ResearchOptions) iter.Seq2[*researcher.Result, error] {
		return func(yield func(*researcher.Result, error) bool) {
			if !yield(&researcher.Result{Progress: "Waiting for research evidence"}, nil) {
				return
			}
			timer := time.NewTimer(50 * time.Second)
			defer timer.Stop()
			select {
			case <-timer.C:
				yield(&researcher.Result{Content: "Completed long research"}, nil)
			case <-ctx.Done():
				yield(nil, ctx.Err())
			}
		}
	})
	httpServer := httptest.NewServer(s)
	defer func() { httpServer.CloseClientConnections(); httpServer.Close() }()
	ctx, cancel := context.WithTimeout(t.Context(), 70*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "long-research-test", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	params := &mcp.CallToolParams{Name: research.ToolName, Arguments: map[string]any{"instructions": "long research"}}
	params.SetProgressToken("long-request")
	result, err := session.CallTool(ctx, params)
	if err != nil || result.IsError || result.Content[0].(*mcp.TextContent).Text != "Completed long research" {
		t.Fatalf("result=%+v error=%v", result, err)
	}
}

type liveResearchResult struct {
	Provider     string               `json:"provider"`
	Milliseconds int64                `json:"milliseconds"`
	Updates      []liveResearchUpdate `json:"updates"`
	Report       string               `json:"report"`
	Error        string               `json:"error"`
}

// Opt in: this makes billable provider calls and opens a local HTTP MCP server.
func TestLiveResearchProgress(t *testing.T) {
	if os.Getenv("RESEARCH_LIVE") != "1" {
		t.Skip("set RESEARCH_LIVE=1 for live research MCP tests")
	}
	_ = godotenv.Load("../../../.env")
	model := os.Getenv("RESEARCH_LIVE_OPENAI_MODEL")
	if model == "" {
		model = "gpt-6-luna"
	}
	claudeModel := os.Getenv("RESEARCH_LIVE_ANTHROPIC_MODEL")
	if claudeModel == "" {
		claudeModel = "claude-sonnet-5-5"
	}
	const question = "Read https://modelcontextprotocol.io/specification/2025-03-26/basic/utilities/progress and https://modelcontextprotocol.io/specification/2025-03-26/basic/utilities/cancellation. Compare progress reporting and cancellation for that exact specification version. Explain progressToken, how numeric progress changes, when notifications stop, and cancellation propagation. Use those two primary sources. Answer in at most 250 words and cite both URLs."
	var results []liveResearchResult
	for _, tt := range []struct {
		name   string
		key    string
		create func(string) (researcher.Provider, error)
	}{
		{"openai", "OPENAI_API_KEY", func(key string) (researcher.Provider, error) { return openai.New(key, openai.WithModel(model)) }},
		{"anthropic", "ANTHROPIC_API_KEY", func(key string) (researcher.Provider, error) {
			return anthropic.New(key, anthropic.WithModel(claudeModel))
		}},
		{"perplexity", "PERPLEXITY_API_KEY", func(key string) (researcher.Provider, error) { return perplexity.New(key) }},
		{"exa", "EXA_API_KEY", func(key string) (researcher.Provider, error) { return exa.New(key) }},
		{"agent", "OPENAI_API_KEY", func(key string) (researcher.Provider, error) {
			completer, err := modelopenai.NewResponder("https://api.openai.com/v1/", model, modelopenai.WithToken(key))
			if err != nil {
				return nil, err
			}
			searcher, err := searchexa.New(os.Getenv("EXA_API_KEY"))
			if err != nil {
				return nil, err
			}
			scraper, err := scrapeexa.New(os.Getenv("EXA_API_KEY"))
			if err != nil {
				return nil, err
			}
			return agent.New(completer, searcher, agent.WithScraper(scraper), agent.WithEffort(provider.EffortLow))
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			key := os.Getenv(tt.key)
			if key == "" || (tt.name == "agent" && os.Getenv("EXA_API_KEY") == "") {
				t.Skip("missing credentials for " + tt.name)
			}
			p, err := tt.create(key)
			if err != nil {
				t.Fatal(err)
			}
			tool, err := research.New(otel.NewResearcher(tt.name, tt.name, p))
			if err != nil {
				t.Fatal(err)
			}
			s := newServer(t, "", otel.NewTool("research", tool))
			httpServer := httptest.NewServer(s)
			defer func() {
				httpServer.CloseClientConnections()
				httpServer.Close()
			}()
			ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
			defer cancel()
			result := liveResearchResult{Provider: tt.name}
			var mu sync.Mutex
			start := time.Now()
			client := mcp.NewClient(&mcp.Implementation{Name: "live-research-test", Version: "1.0.0"}, &mcp.ClientOptions{
				ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
					mu.Lock()
					defer mu.Unlock()
					if req.Params.ProgressToken != tt.name || req.Params.Total != 0 {
						t.Error("invalid progress metadata")
					}
					result.Updates = append(result.Updates, liveResearchUpdate{Milliseconds: time.Since(start).Milliseconds(), Progress: req.Params.Progress, Message: req.Params.Message})
					t.Logf("+%s %s", time.Since(start).Round(time.Millisecond), req.Params.Message)
				},
			})
			session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			params := &mcp.CallToolParams{Name: research.ToolName, Arguments: map[string]any{"instructions": question}}
			params.SetProgressToken(tt.name)
			response, err := session.CallTool(ctx, params)
			elapsed := time.Since(start).Milliseconds()
			// Allow queued notification handlers to finish before inspecting output.
			time.Sleep(100 * time.Millisecond)
			mu.Lock()
			defer mu.Unlock()
			result.Milliseconds = elapsed
			if err != nil {
				result.Error = err.Error()
			} else if response.IsError {
				result.Error = fmt.Sprint(response.Content)
			} else {
				for _, content := range response.Content {
					if text, ok := content.(*mcp.TextContent); ok {
						result.Report += text.Text
					}
				}
			}
			results = append(results, result)
			if result.Error != "" {
				t.Fatal(result.Error)
			}
			if len(result.Updates) < 3 {
				t.Errorf("only %d progress updates", len(result.Updates))
			}
			var previous float64
			for _, update := range result.Updates {
				if update.Progress <= previous || update.Milliseconds > elapsed {
					t.Errorf("invalid update: %+v", update)
				}
				previous = update.Progress
			}
			if len(result.Updates) > 0 && result.Updates[len(result.Updates)-1].Message != "Research complete" {
				t.Error("missing completion update")
			}
			lower := strings.ToLower(result.Report)
			for _, term := range []string{"progresstoken", "cancel", "modelcontextprotocol.io/specification/2025-03-26/basic/utilities/progress", "modelcontextprotocol.io/specification/2025-03-26/basic/utilities/cancellation"} {
				if !strings.Contains(lower, term) {
					t.Errorf("report does not address %q", term)
				}
			}
			t.Logf("report: %s", result.Report)
		})
	}
	if path := os.Getenv("RESEARCH_LIVE_OUTPUT"); path != "" {
		data, err := json.Marshal(results, jsontext.WithIndent("  "), json.Deterministic(true))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
