package jsonutil

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strings"
	"testing"
)

func TestUnmarshalPreservesNumbersAndValidatesEntireInput(t *testing.T) {
	const input = `{"id":9007199254740993,"values":[1.234567890123456789,1e400,null]}`
	var value any
	if err := Unmarshal([]byte(input), &value); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(value, json.Deterministic(true))
	if err != nil || string(data) != input {
		t.Fatalf("round trip: %s (%v)", data, err)
	}
	for _, invalid := range []string{input + `{}`, input + ` garbage`, `{"a":1,}`} {
		var value any
		if err := Unmarshal([]byte(invalid), &value); err == nil {
			t.Fatalf("accepted partial input %q: %v", invalid, value)
		}
	}
}

// This decoder deliberately delegates to v2 without setting its own options.
// The caller's lossless-number and unknown-field policies must reach its fields.
type nestedJSON struct {
	Value any `json:"value"`
}

func (v *nestedJSON) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	type plain nestedJSON
	return json.UnmarshalDecode(dec, (*plain)(v))
}

func TestOptionsReachNestedDecoders(t *testing.T) {
	var value struct {
		Nested nestedJSON `json:"nested"`
	}
	if err := UnmarshalRead(strings.NewReader(`{"nested":{"value":9007199254740993}}`), &value); err != nil {
		t.Fatal(err)
	}
	number, ok := value.Nested.Value.(Number)
	if !ok || number.String() != "9007199254740993" {
		t.Fatalf("lost number: %#v", value)
	}
	if err := Unmarshal([]byte(`{"nested":{"value":1,"unexpected":true}}`), &value, json.RejectUnknownMembers(true)); err == nil {
		t.Fatal("nested decoder ignored unknown-field policy")
	}
}

func TestStrictSyntax(t *testing.T) {
	for _, input := range []string{
		`{"a":1,"a":2}`, `{"a":1,"\u0061":2}`, `{"nested":{"a":1,"a":2}}`,
		`{"a":1} {}`, `{"a":1} garbage`, "{\"text\":\"\xff\"}",
	} {
		t.Run(input, func(t *testing.T) {
			var value any
			if err := Unmarshal([]byte(input), &value); err == nil {
				t.Fatal("accepted invalid input")
			}
			if err := UnmarshalRead(strings.NewReader(input), &value); err == nil {
				t.Fatal("accepted invalid reader input")
			}
		})
	}
}
