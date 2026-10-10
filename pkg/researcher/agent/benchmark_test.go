package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
	"github.com/adrianliechti/wingman/pkg/scraper"
	"github.com/adrianliechti/wingman/pkg/searcher"
)

// Fixed evidence, real model. Opt in because this makes billable model calls.
// It never invokes an external search, extraction, or research service.
//
//go:embed testdata/research-cases.json
var benchmarkCasesJSON []byte

type benchmarkCase struct {
	ID, Question string
	Expected     []struct {
		Answer  string
		Sources []string
	}
	Searches []struct {
		Terms   []string
		Results []searcher.Result
	}
	Pages map[string]*string
}

type benchmarkMetrics struct {
	Case                                                          string
	Repeat                                                        int
	ModelCalls, ToolCalls, SearchRequests, FetchRequests          int
	InputTokens, CachedInputTokens, OutputTokens, ToolResultChars int
	AnswerRecall, CitationRecall                                  float64
	Milliseconds                                                  int64
	Answer, Error                                                 string
	Calls                                                         []provider.ToolCall
}

type benchmarkEvidence struct {
	fixture  benchmarkCase
	metrics  *benchmarkMetrics
	mu       sync.Mutex
	observed map[string]bool
}

func (b *benchmarkEvidence) Capabilities() searcher.Capabilities {
	return searcher.Capabilities{DateFilters: true}
}

func (b *benchmarkEvidence) Search(ctx context.Context, query string, options *searcher.SearchOptions) ([]searcher.Result, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.metrics.SearchRequests++
	var results []searcher.Result
	for _, rule := range b.fixture.Searches {
		match := false
		for _, term := range rule.Terms {
			match = match || strings.Contains(strings.ToLower(query), term)
		}
		if !match {
			continue
		}
		for _, hit := range rule.Results {
			parsed, _ := url.Parse(hit.Source)
			allowed := len(options.Include) == 0
			for _, domain := range options.Include {
				allowed = allowed || parsed.Hostname() == domain
			}
			for _, domain := range options.Exclude {
				if parsed.Hostname() == domain {
					allowed = false
				}
			}
			if allowed {
				results = append(results, hit)
			}
		}
	}
	if options.Limit != nil && len(results) > *options.Limit {
		results = results[:*options.Limit]
	}
	for _, hit := range results {
		b.observed[hit.Source] = true
	}
	return results, nil
}

func (b *benchmarkEvidence) Scrape(ctx context.Context, source string, options *scraper.ScrapeOptions) (*scraper.Document, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.metrics.FetchRequests++
	page := b.fixture.Pages[source]
	if page == nil {
		return nil, errors.New("source unavailable")
	}
	b.observed[source] = true
	return &scraper.Document{Text: *page}, nil
}

type benchmarkCompleter struct {
	provider.Completer
	metrics *benchmarkMetrics
	seen    int
}

func (b *benchmarkCompleter) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	b.metrics.ModelCalls++
	for _, message := range messages[b.seen:] {
		if result, ok := message.ToolResult(); ok {
			for _, part := range result.Parts {
				b.metrics.ToolResultChars += utf8.RuneCountInString(part.Text)
			}
		}
	}
	b.seen = len(messages)
	limited := *options
	maxTokens := 2048
	limited.MaxTokens = &maxTokens
	return func(yield func(*provider.Completion, error) bool) {
		acc := provider.CompletionAccumulator{}
		for completion, err := range b.Completer.Complete(ctx, messages, &limited) {
			if completion != nil {
				acc.Add(*completion)
			}
			if !yield(completion, err) {
				break
			}
		}
		result := acc.Result()
		if result.Message != nil {
			calls := result.Message.ToolCalls()
			b.metrics.ToolCalls += len(calls)
			b.metrics.Calls = append(b.metrics.Calls, calls...)
		}
		if usage := result.Usage; usage != nil {
			b.metrics.InputTokens += usage.InputTokens
			b.metrics.CachedInputTokens += usage.CacheReadInputTokens
			b.metrics.OutputTokens += usage.OutputTokens
		}
	}
}

func TestResearchFixedBenchmark(t *testing.T) {
	if os.Getenv("RESEARCH_BENCHMARK") != "1" {
		t.Skip("set RESEARCH_BENCHMARK=1 for real-model fixed-evidence benchmark")
	}
	endpoint := os.Getenv("RESEARCH_BENCHMARK_URL")
	if endpoint == "" {
		endpoint = "http://localhost:4242/v1/"
	}
	model := os.Getenv("RESEARCH_BENCHMARK_MODEL")
	if model == "" {
		model = "gpt-5.4-mini"
	}
	repeats, _ := strconv.Atoi(os.Getenv("RESEARCH_BENCHMARK_REPEATS"))
	if repeats < 1 {
		repeats = 2
	}
	completer, err := openai.NewResponder(endpoint, model, openai.WithToken(os.Getenv("RESEARCH_BENCHMARK_TOKEN")), openai.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	var cases []benchmarkCase
	if err := json.Unmarshal(benchmarkCasesJSON, &cases); err != nil {
		t.Fatal(err)
	}
	var results []benchmarkMetrics
	for repeat := range repeats {
		for _, fixture := range cases {
			metrics := benchmarkMetrics{Case: fixture.ID, Repeat: repeat + 1}
			t.Run(fmt.Sprintf("%s/%d", fixture.ID, repeat+1), func(t *testing.T) {
				evidence := &benchmarkEvidence{fixture: fixture, metrics: &metrics, observed: map[string]bool{}}
				client, err := New(&benchmarkCompleter{Completer: completer, metrics: &metrics}, evidence, WithScraper(evidence), WithEffort(provider.EffortLow))
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
				defer cancel()
				start := time.Now()
				result, err := client.Research(ctx, fixture.Question, nil)
				metrics.Milliseconds = time.Since(start).Milliseconds()
				if err != nil {
					metrics.Error = err.Error()
					t.Errorf("Research: %v", err)
				} else {
					metrics.Answer = result.Content
					metrics.AnswerRecall, metrics.CitationRecall = gradeBenchmark(fixture, result.Content, evidence.observed)
				}
				t.Logf("tools=%d requests=%d input=%d answer=%.2f citations=%.2f elapsed=%dms", metrics.ToolCalls, metrics.SearchRequests+metrics.FetchRequests, metrics.InputTokens, metrics.AnswerRecall, metrics.CitationRecall, metrics.Milliseconds)
			})
			results = append(results, metrics)
		}
	}
	if path := os.Getenv("RESEARCH_BENCHMARK_OUTPUT"); path != "" {
		data, err := json.MarshalIndent(struct {
			Model   string
			Repeats int
			Results []benchmarkMetrics
		}{model, repeats, results}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

// A narrow regression grader: expected literal facts plus expected observed
// URLs. This does not measure semantic entailment or penalize all extra claims.
func gradeBenchmark(fixture benchmarkCase, answer string, observed map[string]bool) (float64, float64) {
	lower := strings.ToLower(answer)
	if len(fixture.Expected) == 0 {
		lower = strings.ReplaceAll(lower, "’", "'")
		for _, phrase := range []string{"no evidence", "could not find", "couldn't find", "cannot find", "could not verify", "unable to", "abstain"} {
			if strings.Contains(lower, phrase) {
				return 1, 1
			}
		}
		return 0, 0
	}
	facts, citations := 0.0, 0.0
	for _, expected := range fixture.Expected {
		// Formatting labels are optional; exact entities, values and units remain required.
		value := expected.Answer
		if _, after, ok := strings.Cut(value, ": "); ok {
			value = after
		}
		if strings.Contains(lower, strings.ToLower(value)) {
			facts++
		}
		cited := true
		for _, source := range expected.Sources {
			cited = cited && observed[source] && strings.Contains(answer, "]("+source+")")
		}
		if cited {
			citations++
		}
	}
	if strings.Contains(lower, "999 grams") || strings.Contains(lower, "fake.example") {
		return 0, 0
	}
	return facts / float64(len(fixture.Expected)), citations / float64(len(fixture.Expected))
}

func TestGradeBenchmark_NormalizesAbstentionApostrophes(t *testing.T) {
	for _, test := range []struct {
		answer string
		want   float64
	}{
		{"I couldn't find evidence of the population.", 1},
		{"I couldn’t find evidence of the population.", 1},
		{"Nevermere has 123 residents.", 0},
	} {
		t.Run(test.answer, func(t *testing.T) {
			answer, citations := gradeBenchmark(benchmarkCase{}, test.answer, nil)
			if answer != test.want || citations != test.want {
				t.Fatalf("answer=%v citations=%v, want both %v", answer, citations, test.want)
			}
		})
	}
}
