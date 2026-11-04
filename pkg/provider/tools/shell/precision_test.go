package shell

import (
	"encoding/json/v2"
	"testing"
)

func TestNativeArgumentsPreserveNumbers(t *testing.T) {
	for _, tc := range []struct {
		convert func(string) map[string]any
		input   string
	}{
		{BashInput, `{"command":"pwd","limit":9007199254740993}`},
		{ShellAction, `{"commands":["pwd"],"timeout_ms":9007199254740993}`},
		{LocalShellAction, `{"command":["pwd"],"timeout_ms":9007199254740993,"type":"exec"}`},
	} {
		data, err := json.Marshal(tc.convert(tc.input), json.Deterministic(true))
		if err != nil || string(data) != tc.input {
			t.Fatalf("arguments changed: %s (%v)", data, err)
		}
	}
}
