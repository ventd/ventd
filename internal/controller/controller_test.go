package controller

import (
	"strings"
	"testing"
)

func TestParseNvidiaIndex(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    uint
		wantErr bool
	}{
		{"valid_zero", "0", 0, false},
		{"valid_positive", "3", 3, false},
		{"leading_zero", "007", 7, false},
		{"empty", "", 0, true},
		{"non_numeric", "gpu0", 0, true},
		{"negative", "-1", 0, true},
		{"whitespace", " 0", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseNvidiaIndex(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got nil (value=%d)", got)
				}
				if !strings.Contains(err.Error(), tc.input) && tc.input != "" {
					t.Errorf("error %q does not echo input %q", err.Error(), tc.input)
				}
				if tc.input == "" && !strings.Contains(err.Error(), `""`) {
					t.Errorf("error %q does not quote empty input", err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}
