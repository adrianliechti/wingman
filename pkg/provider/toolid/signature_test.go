package toolid

import "testing"

func TestStripSignature(t *testing.T) {
	tests := map[string]struct {
		input string
		want  string
	}{
		"plain id":          {input: "call_1", want: "call_1"},
		"name without sig":  {input: "call_1::search", want: "call_1::search"},
		"valid signature":   {input: "call_1::search::U0VDUkVU", want: "call_1::search"},
		"empty signature":   {input: "call_1::search::", want: "call_1::search"},
		"invalid signature": {input: "call_1::search::%%%", want: "call_1::search::%%%"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if got := StripSignature(test.input); got != test.want {
				t.Fatalf("StripSignature(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}
