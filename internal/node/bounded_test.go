package node

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

func TestBoundedCaptureConcurrentLimit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	capture := &boundedCapture{limit: 1024, cancel: cancel}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_, _ = capture.Write([]byte(strings.Repeat("a", 128)))
			}
		}()
	}
	wg.Wait()
	if capture.buf.Len() != 1024 || !capture.exceeded || ctx.Err() == nil {
		t.Fatalf("capture %d exceeded %v canceled %v", capture.buf.Len(), capture.exceeded, ctx.Err())
	}
}

func TestSSHBoundedTransportAndNoTTY(t *testing.T) {
	if filepath.Separator == '\\' {
		t.Skip("POSIX fake SSH")
	}
	dir := t.TempDir()
	script := `#!/bin/sh
printf 'machine-output'
printf 'separate-stderr' >&2
`
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	args := sshArgs(model.Node{Address: "node.example"})
	found := false
	for _, arg := range args {
		if arg == "-T" {
			found = true
		}
	}
	if !found {
		t.Fatal("machine transport permits inherited TTY")
	}
	out, err := (SSH{}).RunBounded(context.Background(), model.Node{Address: "node.example"}, "true", nil, 64)
	if err != nil || out != "machine-output" {
		t.Fatalf("stdout=%q err=%v", out, err)
	}
	script = `#!/bin/sh
printf 'Permission denied (publickey).' >&2
exit 255
`
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	out, err = (SSH{}).RunBounded(context.Background(), model.Node{Address: "node.example"}, "true", nil, 64)
	if err == nil || !strings.Contains(err.Error(), "Permission denied (publickey).") || out != "" {
		t.Fatalf("lost stderr or polluted stdout=%q err=%v", out, err)
	}
	script = `#!/bin/sh
while :; do printf '0123456789012345678901234567890123456789'; done
`
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err = (SSH{}).RunBounded(ctx, model.Node{Address: "node.example"}, "true", nil, 1024)
	if !errors.Is(err, ErrOutputLimit) || len(out) > 1024 {
		t.Fatalf("bounded output=%d err=%v", len(out), err)
	}
}
