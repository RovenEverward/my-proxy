package cli

import "testing"

func TestControlURL(t *testing.T) {
	ok := map[string]string{
		"203.0.113.10":                "https://203.0.113.10:8080",
		"203.0.113.10:9443":           "https://203.0.113.10:9443",
		" cp.example.net ":            "https://cp.example.net:8080",
		"https://cp.example.net:8080": "https://cp.example.net:8080",
		"https://cp.example.net":      "https://cp.example.net:8080",
		"[2001:db8::1]:8080":          "https://[2001:db8::1]:8080",
		"2001:db8::1":                 "https://[2001:db8::1]:8080",
	}
	for in, want := range ok {
		got, _, err := controlURL(in)
		if err != nil || got != want {
			t.Errorf("controlURL(%q) = %q, %v;want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"http://203.0.113.10:8080", "", "https://"} {
		if _, _, err := controlURL(bad); err == nil {
			t.Errorf("controlURL(%q) 应当报错", bad)
		}
	}
}
