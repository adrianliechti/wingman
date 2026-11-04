package jsonutil

import (
	jsonv1 "encoding/json"
	"encoding/json/jsontext"
	"strconv"
	"strings"
)

// IsInteger checks the exact decimal value, including exponent notation.
func IsInteger(value jsonv1.Number) bool {
	_, _, _, ok := integerParts(value)
	return ok
}

// Int64 accepts any JSON number whose exact value is an integer in int64 range.
func Int64(value jsonv1.Number) (int64, bool) {
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
func integerParts(value jsonv1.Number) (digits string, zeros int, negative, ok bool) {
	s := value.String()
	if len(s) == 0 || !(s[0] == '-' || s[0] >= '0' && s[0] <= '9') || !jsontext.Value(s).IsValid() {
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
