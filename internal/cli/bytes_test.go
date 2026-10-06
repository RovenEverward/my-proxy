package cli

import (
	"reflect"
	"testing"
	"time"
)

func TestParseBytes(t *testing.T) {
	cases := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{in: "100GB", want: 107374182400},
		{in: "1.5TB", want: 1649267441664},
		{in: "500MB", want: 524288000},
		{in: "1024", want: 1024},
		{in: "0", want: 0},
		{in: "1B", want: 1},
		{in: "512b", want: 512},
		{in: "1kb", want: 1024},
		{in: "2 KB", want: 2048},
		{in: "1.5KB", want: 1536},
		{in: " 10 GB ", want: 10737418240},
		{in: "", wantErr: true},
		{in: "   ", wantErr: true},
		{in: "GB", wantErr: true},
		{in: "abc", wantErr: true},
		{in: "1XB", wantErr: true},
		{in: "-5GB", wantErr: true},
		{in: "-1", wantErr: true},
		{in: "99999999999TB", wantErr: true},
		{in: "9223372036854775808", wantErr: true},
	}
	for _, tc := range cases {
		got, err := parseBytes(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseBytes(%q) = %d, 期望报错", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseBytes(%q) 报错: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseBytes(%q) = %d, 期望 %d", tc.in, got, tc.want)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0B"},
		{-1, "0B"},
		{512, "512B"},
		{1024, "1KB"},
		{1536, "1.5KB"},
		{1610612736, "1.5GB"},
		{107374182400, "100GB"},
		{1099511627776, "1TB"},
	}
	for _, tc := range cases {
		if got := humanBytes(tc.in); got != tc.want {
			t.Errorf("humanBytes(%d) = %q, 期望 %q", tc.in, got, tc.want)
		}
	}
}

func TestParseExpire(t *testing.T) {
	got, err := parseExpire("2026-10-07")
	if err != nil {
		t.Fatalf("parseExpire 报错: %v", err)
	}
	want := time.Date(2026, 10, 7, 23, 59, 59, 0, time.Local)
	if !got.Equal(want) {
		t.Errorf("parseExpire = %v, 期望 %v", got, want)
	}
	if got.Location() != time.Local {
		t.Errorf("parseExpire 的时区 = %v, 期望当地时区", got.Location())
	}

	for _, in := range []string{"", "2026/10/07", "2026-13-01", "20261007", "2026-10-07T00:00:00Z"} {
		if _, err := parseExpire(in); err == nil {
			t.Errorf("parseExpire(%q) 期望报错", in)
		}
	}
}

func TestParseInboundIDs(t *testing.T) {
	cases := []struct {
		in      string
		want    []int64
		wantErr bool
	}{
		{in: "", want: nil},
		{in: "1,2,3", want: []int64{1, 2, 3}},
		{in: " 1 , 2 ", want: []int64{1, 2}},
		{in: "1,,2", want: []int64{1, 2}},
		{in: ",", want: nil},
		{in: "x", wantErr: true},
		{in: "1,y", wantErr: true},
	}
	for _, tc := range cases {
		got, err := parseInboundIDs(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseInboundIDs(%q) = %v, 期望报错", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseInboundIDs(%q) 报错: %v", tc.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("parseInboundIDs(%q) = %v, 期望 %v", tc.in, got, tc.want)
		}
	}
}

func TestMaskToken(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "(未设置)"},
		{"abc", "abc"},
		{"abcdef", "abcdef"},
		{"abcdefg", "abcdef…"},
		{"abcdefghijklmn", "abcdef…"},
	}
	for _, tc := range cases {
		if got := maskToken(tc.in); got != tc.want {
			t.Errorf("maskToken(%q) = %q, 期望 %q", tc.in, got, tc.want)
		}
	}
}
