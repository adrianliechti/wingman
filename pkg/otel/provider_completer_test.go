package otel

import (
	"context"
	"iter"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// deltaTemporality must select delta for the monotonic instruments insights
// sums (counters, histograms) and cumulative for up/down counters where delta
// is ill-defined. gen_ai.client.token.usage is a histogram, so it gets delta.
func TestDeltaTemporalitySelector(t *testing.T) {
	delta := map[sdkmetric.InstrumentKind]bool{
		sdkmetric.InstrumentKindCounter:           true,
		sdkmetric.InstrumentKindHistogram:         true,
		sdkmetric.InstrumentKindObservableCounter: true,

		sdkmetric.InstrumentKindUpDownCounter:           false,
		sdkmetric.InstrumentKindObservableUpDownCounter: false,
		sdkmetric.InstrumentKindObservableGauge:         false,
	}

	for kind, wantDelta := range delta {
		got := deltaTemporality(kind)
		want := metricdata.CumulativeTemporality
		if wantDelta {
			want = metricdata.DeltaTemporality
		}
		if got != want {
			t.Errorf("kind %v: got %v, want %v", kind, got, want)
		}
	}
}

type fakeCompleter struct {
	usage *provider.Usage
}

func (f *fakeCompleter) Complete(ctx context.Context, messages []provider.Message, options *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(yield func(*provider.Completion, error) bool) {
		yield(&provider.Completion{
			Model:   "test-model",
			Status:  provider.CompletionStatusCompleted,
			Message: &provider.Message{Role: provider.MessageRoleAssistant, Content: []provider.Content{provider.TextContent("hi")}},
			Usage:   f.usage,
		}, nil)
	}
}

// Under delta temporality (the insights path), each request must contribute only
// its own tokens. Summing the delta datapoints across collection windows must
// equal the true running total — never re-reporting earlier requests. This is
// the regression guard against miscounting following requests.
func TestTokenUsageDeltaSumsAcrossRequests(t *testing.T) {
	reader := sdkmetric.NewManualReader(sdkmetric.WithTemporalitySelector(deltaTemporality))
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(mp)
	defer otel.SetMeterProvider(prev)

	ctx := context.Background()
	fc := &fakeCompleter{}
	// A single completer instance reused across requests — the realistic case
	// where the metric instruments are long-lived and shared.
	c := NewCompleter("anthropic", "test-model", fc)

	drain := func() {
		for _, err := range c.Complete(ctx, nil, nil) {
			if err != nil {
				t.Fatalf("complete: %v", err)
			}
		}
	}

	collect := func() (input, output int64) {
		var rm metricdata.ResourceMetrics
		if err := reader.Collect(ctx, &rm); err != nil {
			t.Fatalf("collect: %v", err)
		}
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				if m.Name != "gen_ai.client.token.usage" {
					continue
				}
				h, ok := m.Data.(metricdata.Histogram[int64])
				if !ok {
					t.Fatalf("token usage metric is %T, want Histogram[int64]", m.Data)
				}
				for _, dp := range h.DataPoints {
					tt, _ := dp.Attributes.Value(attribute.Key("gen_ai.token.type"))
					switch tt.AsString() {
					case "input":
						input += dp.Sum
					case "output":
						output += dp.Sum
					}
				}
			}
		}
		return input, output
	}

	// Window 1: two requests before the first collect.
	fc.usage = &provider.Usage{InputTokens: 100, OutputTokens: 10}
	drain()
	fc.usage = &provider.Usage{InputTokens: 50, OutputTokens: 5}
	drain()

	in1, out1 := collect()
	if in1 != 150 || out1 != 15 {
		t.Fatalf("window 1 delta: input=%d output=%d, want 150/15", in1, out1)
	}

	// Window 2: a following request. Its delta must be ITS OWN tokens only —
	// the earlier 150/15 must not be re-reported.
	fc.usage = &provider.Usage{InputTokens: 30, OutputTokens: 3}
	drain()

	in2, out2 := collect()
	if in2 != 30 || out2 != 3 {
		t.Fatalf("window 2 delta: input=%d output=%d, want 30/3 (earlier requests must not repeat)", in2, out2)
	}

	// Summing deltas across windows = true running total.
	if in1+in2 != 180 || out1+out2 != 18 {
		t.Fatalf("summed deltas: input=%d output=%d, want 180/18", in1+in2, out1+out2)
	}
}
