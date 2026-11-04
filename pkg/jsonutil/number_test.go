package jsonutil

import (
	"encoding/json/v2"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"testing"
)

func TestExactIntegerNumbers(t *testing.T) {
	for _, tc := range []struct {
		number string
		want   int64
	}{
		{"0", 0}, {"-0.0e999999999999999999999", 0}, {"3.0", 3}, {"30e-1", 3},
		{"9007199254740993.0", 9007199254740993}, {"90071992547409930e-1", 9007199254740993},
		{"9223372036854775807", 9223372036854775807}, {"-9223372036854775808", -9223372036854775808},
	} {
		got, ok := Int64(Number(tc.number))
		if !ok || got != tc.want || !IsInteger(Number(tc.number)) {
			t.Errorf("%s: %d, %v", tc.number, got, ok)
		}
	}
	for _, n := range []Number{"1e400", "1e999999999999999999999", "9223372036854775808"} {
		if !IsInteger(n) {
			t.Errorf("%s should be integral", n)
		}
		if _, ok := Int64(n); ok {
			t.Errorf("%s should overflow", n)
		}
	}
	for _, n := range []Number{"1.0000000000000000001", "1e-400", "1e-999999999999999999999", "true", "null", "2/1", "01", "", "1.1"} {
		if IsInteger(n) {
			t.Errorf("%s should not be integral", n)
		}
		if _, ok := Int64(n); ok {
			t.Errorf("%s should not convert", n)
		}
	}
}

func TestNumberCodecs(t *testing.T) {
	for _, text := range []string{"0", "-0", "9007199254740993", "1.234567890123456789", "1e400", "-1E-999999999999999999"} {
		for _, stringify := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/string=%t", text, stringify), func(t *testing.T) {
				option := json.StringifyNumbers(stringify)
				want := text
				if stringify {
					want = strconv.Quote(text)
				}
				data, err := json.Marshal(Number(text), option)
				if err != nil || string(data) != want {
					t.Fatalf("encode: %s (%v), want %s", data, err, want)
				}
				var number Number
				if err := json.Unmarshal(data, &number, option); err != nil || string(number) != text {
					t.Fatalf("decode: %q (%v)", number, err)
				}
			})
		}
	}
	for _, text := range []string{"", " 1", "1 ", "01", "+1", "1e", "NaN", "null", "true", `"1"`, "[1]", "1 2"} {
		if _, err := json.Marshal(Number(text)); err == nil {
			t.Errorf("encoded invalid number %q", text)
		}
		if err := json.Unmarshal([]byte(strconv.Quote(text)), new(Number), json.StringifyNumbers(true)); err == nil {
			t.Errorf("decoded invalid number %q", text)
		}
	}
	if err := json.Unmarshal([]byte(`"1"`), new(Number)); err == nil {
		t.Fatal("accepted string without stringify option")
	}
	if err := json.Unmarshal([]byte(`1`), new(Number), json.StringifyNumbers(true)); err == nil {
		t.Fatal("accepted unquoted stringified number")
	}
	value := Number("123")
	if err := json.Unmarshal([]byte("null"), &value); err != nil || value != "" {
		t.Fatalf("null did not reset value: %q (%v)", value, err)
	}
}

func FuzzExactIntegerNumbers(f *testing.F) {
	for _, coefficient := range []int64{0, 1, -1, 10, 9007199254740993, math.MinInt64, math.MaxInt64} {
		f.Add(coefficient, int8(0), uint8(0))
		f.Add(coefficient, int8(-1), uint8(1))
		f.Add(coefficient, int8(127), uint8(5))
	}
	f.Fuzz(func(t *testing.T, coefficient int64, exponent int8, fraction uint8) {
		digits := strconv.FormatInt(coefficient, 10)
		sign := ""
		if coefficient < 0 {
			sign, digits = "-", digits[1:]
		}
		if places := int(fraction) % len(digits); places > 0 {
			split := len(digits) - places
			digits = digits[:split] + "." + digits[split:]
		}
		number := Number(fmt.Sprintf("%s%se%d", sign, digits, exponent))
		// Use exact rational arithmetic as an independent reference. The int8
		// exponent bounds memory use even while fuzzing adversarial inputs.
		want, ok := new(big.Rat).SetString(string(number))
		if !ok {
			t.Fatalf("invalid generated number %q", number)
		}
		if got := IsInteger(number); got != want.IsInt() {
			t.Errorf("IsInteger(%s) = %v, want %v", number, got, want.IsInt())
		}
		got, ok := Int64(number)
		fits := want.IsInt() && want.Num().IsInt64()
		if ok != fits || ok && got != want.Num().Int64() {
			t.Errorf("Int64(%s) = %d, %v; want %s, %v", number, got, ok, want, fits)
		}
		data, err := json.Marshal(number)
		if err != nil {
			t.Fatal(err)
		}
		var decoded Number
		if err := json.Unmarshal(data, &decoded); err != nil || decoded != number {
			t.Fatalf("number changed: %s -> %s (%v)", number, decoded, err)
		}
	})
}
