package jsonutil

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"strconv"
	"strings"
)

// Number retains the exact decimal spelling of a JSON number, including values
// outside float64's precision and range. It must contain a valid JSON numeric
// literal and serializes as a number, not a string; its empty zero value is invalid.
type Number string

func (n Number) String() string { return string(n) }

func (n Number) Float64() (float64, error) { return strconv.ParseFloat(string(n), 64) }

func (n Number) Int64() (int64, error) { return strconv.ParseInt(string(n), 10, 64) }

func (n Number) valid() bool {
	return len(n) > 0 && (n[0] == '-' || n[0] >= '0' && n[0] <= '9') &&
		strings.TrimSpace(string(n)) == string(n) && jsontext.Value(n).IsValid()
}

func (n Number) MarshalJSONTo(enc *jsontext.Encoder) error {
	if !n.valid() {
		return fmt.Errorf("invalid JSON number %q", n)
	}
	if stringify, _ := json.GetOption(enc.Options(), json.StringifyNumbers); stringify {
		return enc.WriteToken(jsontext.String(string(n)))
	}
	return enc.WriteValue(jsontext.Value(n))
}

func (n *Number) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	token, err := dec.ReadToken()
	if err != nil {
		return err
	}
	if token.Kind() == 'n' {
		*n = ""
		return nil
	}
	stringify, _ := json.GetOption(dec.Options(), json.StringifyNumbers)
	if stringify && token.Kind() != '"' {
		return fmt.Errorf("expected a stringified JSON number")
	}
	if !stringify && token.Kind() != '0' {
		return fmt.Errorf("expected a JSON number")
	}
	value := Number(token.String())
	// ReadToken already validates number literals; quoted numbers need checking.
	if stringify && !value.valid() {
		return fmt.Errorf("invalid JSON number %q", value)
	}
	*n = value
	return nil
}

// IsInteger checks the exact decimal value, including exponent notation.
func IsInteger(value Number) bool {
	_, _, _, ok := integerParts(value)
	return ok
}

// Int64 accepts any JSON number whose exact value is an integer in int64 range.
func Int64(value Number) (int64, bool) {
	digits, zeros, negative, ok := integerParts(value)
	if !ok || zeros > 19 || len(digits) > 19-zeros {
		return 0, false
	}
	if negative {
		digits = "-" + digits
	}
	number, err := strconv.ParseInt(digits+strings.Repeat("0", zeros), 10, 64)
	return number, err == nil
}

// Inspect decimal digits without expanding powers of ten. Even a valid JSON
// number with an enormous exponent should take space proportional to its text.
func integerParts(value Number) (digits string, zeros int, negative, ok bool) {
	s := value.String()
	if !value.valid() {
		return "", 0, false, false
	}
	negative = s[0] == '-'
	if negative {
		s = s[1:]
	}
	mantissa := s
	var exponent int64
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		mantissa = s[:i]
		var err error
		exponent, err = strconv.ParseInt(s[i+1:], 10, 64)
		// ParseInt saturates on overflow. The bounds below are sufficient
		// for integrality and int64 conversion, so no big integer is needed.
		if err != nil && s[i+1] == '-' {
			exponent = -1 << 63
		}
	}
	fraction := 0
	if i := strings.IndexByte(mantissa, '.'); i >= 0 {
		fraction = len(mantissa) - i - 1
		mantissa = mantissa[:i] + mantissa[i+1:]
	}
	digits = strings.TrimLeft(mantissa, "0")
	if digits == "" {
		return "0", 0, negative, true
	}
	trimmed := strings.TrimRight(digits, "0")
	trailing := len(digits) - len(trimmed)
	digits = trimmed
	if exponent > int64(len(s))+19 {
		return digits, 20, negative, true
	}
	if exponent < -int64(len(s))-19 {
		return "", 0, negative, false
	}
	zeros = int(exponent) + trailing - fraction
	return digits, zeros, negative, zeros >= 0
}
