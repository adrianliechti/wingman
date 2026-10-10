package tool

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
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

func TestIntegerParameter_ValidatesBeforeConversion(t *testing.T) {
	for _, test := range []struct {
		value any
		want  int
	}{{nil, 5}, {3, 3}, {float64(3), 3}, {json.Number("3"), 3}, {50.0, 10}, {int(^uint(0) >> 1), 10}} {
		got, err := IntegerParameter(map[string]any{"limit": test.value}, "limit", 5, 1, 10)
		if err != nil || got != test.want {
			t.Fatalf("value %v: got %d, err %v", test.value, got, err)
		}
	}
	for _, input := range []any{0, -1, 1.5, "3", true, json.Number("invalid"), json.Number("1.5"), math.NaN(), math.Inf(1), float64(int(^uint(0) >> 1))} {
		if _, err := IntegerParameter(map[string]any{"limit": input}, "limit", 5, 1, 10); err == nil {
			t.Fatalf("accepted %v", input)
		}
	}
}
