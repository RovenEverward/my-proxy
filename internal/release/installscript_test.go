package release

import (
	"bytes"
	"os"
	"testing"
)

// TestInstallCLIScriptIsASCII: deploy/install-cli.ps1 must be pure ASCII with no
// BOM, so Windows PowerShell 5.1 runs it both via -File and via irm | iex: -File
// without a BOM decodes UTF-8 as ANSI, and a BOM breaks irm | iex.
func TestInstallCLIScriptIsASCII(t *testing.T) {
	b, err := os.ReadFile("../../deploy/install-cli.ps1")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatal("deploy/install-cli.ps1 starts with a UTF-8 BOM; irm | iex fails on it")
	}
	for i, c := range b {
		if c >= 0x80 {
			line := bytes.Count(b[:i], []byte{'\n'}) + 1
			t.Fatalf("deploy/install-cli.ps1 has non-ASCII byte 0x%02x at line %d, offset %d", c, line, i)
		}
	}
}
