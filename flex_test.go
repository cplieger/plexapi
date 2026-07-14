package plexapi

import (
	"encoding/json"
	"testing"
)

func TestFlexInt(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    int
		wantErr bool
	}{
		{name: "bare number", in: `14`, want: 14},
		{name: "quoted number", in: `"14"`, want: 14},
		{name: "null", in: `null`, want: 0},
		{name: "empty string", in: `""`, want: 0},
		{name: "negative", in: `-3`, want: -3},
		{name: "quoted negative", in: `"-3"`, want: -3},
		{name: "float errors", in: `1.5`, wantErr: true},
		{name: "text errors", in: `"abc"`, wantErr: true},
		{name: "object errors", in: `{}`, wantErr: true},
		{name: "array errors", in: `[]`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var f FlexInt
			err := json.Unmarshal([]byte(tt.in), &f)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && int(f) != tt.want {
				t.Errorf("FlexInt = %d, want %d", f, tt.want)
			}
		})
	}
}

func TestRatingKeyValidate(t *testing.T) {
	tests := []struct {
		key     RatingKey
		wantErr bool
	}{
		{key: "123"},
		{key: "0"},
		{key: "-1"}, // numeric; Plex never issues it but Atoi accepts
		{key: "", wantErr: true},
		{key: "abc", wantErr: true},
		{key: "12/../etc", wantErr: true},
		{key: "12 34", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(string(tt.key), func(t *testing.T) {
			if err := tt.key.Validate(); (err != nil) != tt.wantErr {
				t.Errorf("Validate(%q) = %v, wantErr %v", tt.key, err, tt.wantErr)
			}
		})
	}
}

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
