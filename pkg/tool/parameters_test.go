package tool

import (
	"math"
	"reflect"
	"testing"

	"github.com/adrianliechti/wingman/pkg/jsonutil"
)

func TestParameters_PreserveTextAndValidateArrays(t *testing.T) {
	text := "  Original text\n"
	got, err := StringParameter(map[string]any{"text": text}, "text")
	if err != nil || got != text {
		t.Fatalf("text = %q, err = %v", got, err)
	}
	if _, err := StringParameter(map[string]any{"text": 1}, "text"); err == nil {
		t.Fatal("accepted a number as text")
	}
	for _, input := range []any{[]any{" a ", "b", "a", ""}, []string{" a ", "b", "a", ""}} {
		got, err := StringsParameter(map[string]any{"values": input}, "values")
		if err != nil || !reflect.DeepEqual(got, []string{"a", "b"}) {
			t.Fatalf("values = %v, err = %v", got, err)
		}
	}
	for _, input := range []any{"a", []any{"a", 1}, []any{nil}, 1} {
		if _, err := StringsParameter(map[string]any{"values": input}, "values"); err == nil {
			t.Fatalf("accepted %v", input)
		}
	}
}

func TestIntegerParameterLosslessNumbers(t *testing.T) {
	for _, value := range []jsonutil.Number{"3.0", "3e0", "30e-1"} {
		got, err := IntegerParameter(map[string]any{"limit": value}, "limit", 1, 1, 10)
		if err != nil || got != 3 {
			t.Fatalf("%s: %d (%v)", value, got, err)
		}
	}
	for _, value := range []jsonutil.Number{"1.0000000000000000001", "1e-400", "1e400", "2/1"} {
		if _, err := IntegerParameter(map[string]any{"limit": value}, "limit", 1, 1, 10); err == nil {
			t.Fatalf("accepted %s", value)
		}
	}
}

func TestIntegerParameter_ValidatesBeforeConversion(t *testing.T) {
	for _, test := range []struct {
		value any
		want  int
	}{{nil, 5}, {3, 3}, {float64(3), 3}, {jsonutil.Number("3"), 3}, {50.0, 10}, {int(^uint(0) >> 1), 10}} {
		got, err := IntegerParameter(map[string]any{"limit": test.value}, "limit", 5, 1, 10)
		if err != nil || got != test.want {
			t.Fatalf("value %v: got %d, err %v", test.value, got, err)
		}
	}
	for _, input := range []any{0, -1, 1.5, "3", true, jsonutil.Number("invalid"), jsonutil.Number("1.5"), math.NaN(), math.Inf(1), float64(int(^uint(0) >> 1))} {
		if _, err := IntegerParameter(map[string]any{"limit": input}, "limit", 5, 1, 10); err == nil {
			t.Fatalf("accepted %v", input)
		}
	}
}
