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

func TestFlexInt64(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    int64
		wantErr bool
	}{
		{name: "bare beyond int32", in: `5000000000`, want: 5000000000},
		{name: "quoted beyond int32", in: `"5000000000"`, want: 5000000000},
		{name: "zero", in: `0`, want: 0},
		{name: "null", in: `null`, want: 0},
		{name: "empty string", in: `""`, want: 0},
		{name: "negative", in: `-3`, want: -3},
		{name: "max int64", in: `9223372036854775807`, want: 9223372036854775807},
		{name: "overflow errors", in: `9223372036854775808`, wantErr: true},
		{name: "float errors", in: `1.5`, wantErr: true},
		{name: "text errors", in: `"abc"`, wantErr: true},
		{name: "object errors", in: `{}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := FlexInt64(99)
			err := json.Unmarshal([]byte(tt.in), &f)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Unmarshal(%s) err = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if !tt.wantErr && int64(f) != tt.want {
				t.Errorf("Unmarshal(%s) = %d, want %d", tt.in, f, tt.want)
			}
		})
	}
}

func TestFlexBool(t *testing.T) {
	tests := []struct {
		in        string
		want      bool
		wantValid bool
	}{
		{in: `true`, want: true, wantValid: true},
		{in: `false`, wantValid: true},
		{in: `1`, want: true, wantValid: true},
		{in: `0`, wantValid: true},
		{in: `"1"`, want: true, wantValid: true},
		{in: `"0"`, wantValid: true},
		{in: `"true"`, want: true, wantValid: true},
		{in: `"false"`, wantValid: true},
		{in: `null`, wantValid: true},
		{in: `"maybe"`},
		{in: `{}`},
		{in: `[]`},
		{in: `2`},
		{in: `""`},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			var b FlexBool
			if err := json.Unmarshal([]byte(tt.in), &b); err != nil {
				t.Fatalf("Unmarshal(%s) err = %v, want nil (FlexBool never fails)", tt.in, err)
			}
			if b.Bool() != tt.want || b.Valid() != tt.wantValid {
				t.Errorf("Unmarshal(%s) = Bool %v Valid %v, want Bool %v Valid %v",
					tt.in, b.Bool(), b.Valid(), tt.want, tt.wantValid)
			}
		})
	}
}

// TestFlexBoolReuseResets pins that decoding into a used value overwrites
// both the value and the validity bit.
func TestFlexBoolReuseResets(t *testing.T) {
	var b FlexBool
	for _, step := range []struct {
		in              string
		want, wantValid bool
	}{
		{in: `true`, want: true, wantValid: true},
		{in: `"maybe"`},
		{in: `false`, wantValid: true},
	} {
		if err := json.Unmarshal([]byte(step.in), &b); err != nil {
			t.Fatalf("Unmarshal(%s) err = %v", step.in, err)
		}
		if b.Bool() != step.want || b.Valid() != step.wantValid {
			t.Errorf("after Unmarshal(%s): Bool %v Valid %v, want %v %v", step.in, b.Bool(), b.Valid(), step.want, step.wantValid)
		}
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
