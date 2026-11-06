package flagargs

import (
	"reflect"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	spec := Spec{Values: []string{"short", "names"}, Bools: []string{"force"}, Required: []string{"short"}, Positionals: 1}
	tests := []struct {
		name    string
		args    []string
		wantPos []string
		short   string
		force   bool
		names   string
		wantErr string
	}{
		{name: "positional first", args: []string{"dir", "--short", "abc1234"}, wantPos: []string{"dir"}, short: "abc1234"},
		{name: "flags first", args: []string{"--short", "abc1234", "dir"}, wantPos: []string{"dir"}, short: "abc1234"},
		{name: "equals form and a boolean", args: []string{"dir", "--short=abc1234", "--force"}, wantPos: []string{"dir"}, short: "abc1234", force: true},
		{name: "an empty value is given", args: []string{"dir", "--short", "abc1234", "--names="}, wantPos: []string{"dir"}, short: "abc1234"},
		{name: "missing required", args: []string{"dir"}, wantErr: "--short"},
		{name: "no value", args: []string{"dir", "--short"}, wantErr: "expected one argument"},
		{name: "unknown flag", args: []string{"dir", "--short", "x", "--nope"}, wantErr: "unrecognized"},
		{name: "boolean with a value", args: []string{"dir", "--short", "x", "--force=1"}, wantErr: "ignored explicit"},
		{name: "no positional", args: []string{"--short", "x"}, wantErr: "positional"},
		{name: "two positionals", args: []string{"a", "b", "--short", "x"}, wantErr: "positional"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.args, spec)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Positional, tt.wantPos) || got.Value("short") != tt.short || got.Bool("force") != tt.force || got.Value("names") != tt.names {
				t.Errorf("got %+v", got)
			}
		})
	}
}
