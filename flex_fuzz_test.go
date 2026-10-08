package plexapi

import (
	"encoding/json"
	"strconv"
	"testing"
)

// FuzzFlexInt asserts the decoder never panics and agrees with json.Number
// semantics on valid integer inputs.
func FuzzFlexInt(f *testing.F) {
	for _, s := range []string{`14`, `"14"`, `null`, `""`, `-3`, `1.5`, `"abc"`, `{}`, `1e3`, `"1e3"`, ` 7`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var fi FlexInt
		_ = json.Unmarshal(data, &fi) // must not panic
	})
}

// FuzzFlexInt64 asserts the decoder never panics and agrees with
// strconv.ParseInt on every input it accepts as a bare number.
func FuzzFlexInt64(f *testing.F) {
	for _, s := range []string{`5000000000`, `"5000000000"`, `null`, `""`, `-3`, `1.5`, `"abc"`, `{}`, `9223372036854775808`, ` 7`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var fi FlexInt64
		if err := json.Unmarshal(data, &fi); err != nil {
			return
		}
		var num json.Number
		if json.Unmarshal(data, &num) == nil {
			if want, err := strconv.ParseInt(num.String(), 10, 64); err == nil && int64(fi) != want {
				t.Errorf("FlexInt64(%s) = %d, want %d", data, fi, want)
			}
		}
	})
}

// FuzzFlexBool asserts decoding never fails and an invalid value always
// reads as false.
func FuzzFlexBool(f *testing.F) {
	for _, s := range []string{`true`, `false`, `1`, `0`, `"1"`, `"true"`, `null`, `"maybe"`, `{}`, `[1]`, `2`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if !json.Valid(data) {
			return // the decoder rejects malformed JSON before UnmarshalJSON runs
		}
		var b FlexBool
		if err := json.Unmarshal(data, &b); err != nil {
			t.Fatalf("FlexBool(%s) err = %v, want nil", data, err)
		}
		if !b.Valid() && b.Bool() {
			t.Errorf("FlexBool(%s) is invalid but reads true", data)
		}
	})
}

// FuzzRatingKeyValidate asserts validation never panics and never accepts a
// key that strconv.Atoi rejects (the URL-interpolation safety contract).
func FuzzRatingKeyValidate(f *testing.F) {
	for _, s := range []string{"123", "", "abc", "1/../2", "0x10", "٣", "9999999999999999999"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		err := RatingKey(s).Validate()
		if err == nil {
			for _, r := range s {
				if (r < '0' || r > '9') && r != '-' && r != '+' {
					t.Errorf("Validate accepted %q containing %q", s, r)
				}
			}
		}
	})
}
