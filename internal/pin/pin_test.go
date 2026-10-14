package pin

import (
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	raw := strings.Repeat("A", 43)
	want := Prefix + raw
	for _, in := range []string{want, raw, "sha256:" + raw, "  " + want + "=  "} {
		got, err := Normalize(in)
		if err != nil || got != want {
			t.Errorf("Normalize(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "SHA256:abc", "SHA256:" + strings.Repeat("!", 43), "MD5:" + raw} {
		if _, err := Normalize(bad); err == nil {
			t.Errorf("Normalize(%q) 应当报错", bad)
		}
	}
}
