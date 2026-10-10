package node

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/RovenEverward/my-proxy/internal/model"
)

var ErrOutputLimit = errors.New("command output exceeded limit")

// RunBounded bounds captured stdout and stderr before allocating them. Canceling
// SSH stops the local transport; remote scripts must also own a cleanup watchdog.
func (s SSH) RunBounded(ctx context.Context, n model.Node, script string, stdin []byte, maxBytes int) (string, error) {
	if maxBytes <= 0 || maxBytes > 64<<20 {
		return "", fmt.Errorf("invalid command output limit")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	output := &boundedCapture{limit: maxBytes, cancel: cancel}
	stderr := &boundedCapture{limit: min(maxBytes, 64<<10), cancel: cancel}
	err := runWithIdentityIO(ctx, s.timeout(), n, script, stdin, "", output, stderr)
	output.mu.Lock()
	defer output.mu.Unlock()
	stderr.mu.Lock()
	defer stderr.mu.Unlock()
	if output.exceeded || stderr.exceeded {
		return output.buf.String(), ErrOutputLimit
	}
	return output.buf.String(), err
}

type boundedCapture struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	limit    int
	exceeded bool
	cancel   context.CancelFunc
}

func (b *boundedCapture) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *boundedCapture) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.limit - b.buf.Len()
	if len(p) > remaining {
		if remaining > 0 {
			_, _ = b.buf.Write(p[:remaining])
		}
		b.exceeded = true
		b.cancel()
		return len(p), nil // drain until canceled, without additional allocation
	}
	return b.buf.Write(p)
}
