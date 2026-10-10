package node

import (
	"context"
	"fmt"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/model"
)

const singBoxBin = `SING_BOX_BIN="${MPX_SING_BOX_BIN:-/usr/local/lib/mpx/sing-box}"; `

// DeploySingBox validates a staged file before touching the running service.
// It explicitly stops all QUIC connections, then commits the accounting reset
// before starting the new process. The callbacks run only at those boundaries.
func DeploySingBox(ctx context.Context, r Runner, n model.Node, d model.CoreDeployment, cfg []byte, beforeStop, afterStop func() error) error {
	p := d.ConfigPathOrDefault()
	staged := p + ".next"
	backup := p + ".bak"
	if err := StageSingBoxConfig(ctx, r, n, d, cfg); err != nil {
		return err
	}
	if beforeStop != nil {
		if err := beforeStop(); err != nil {
			return fmt.Errorf("final sing-box sample failed; configuration not applied: %w", err)
		}
	}
	if err := CoreUnit(ctx, r, n, d, "stop"); err != nil {
		return err
	}
	if afterStop != nil {
		if err := afterStop(); err != nil {
			return fmt.Errorf("sing-box stopped; accounting baseline failed: %w", err)
		}
	}
	if _, err := r.Run(ctx, n, "if test -f "+shellQuote(p)+"; then cp -f "+shellQuote(p)+" "+shellQuote(backup)+"; fi; mv -f "+shellQuote(staged)+" "+shellQuote(p)); err != nil {
		return err
	}
	startErr := CoreUnit(ctx, r, n, d, "start")
	if startErr == nil {
		if _, err := r.Run(ctx, n, "sleep 1"); err != nil {
			return err
		}
		active, err := CoreIsActive(ctx, r, n, d)
		if err != nil {
			return err
		}
		if active {
			return nil
		}
	}
	// A prior configuration is retained for an explicit operator rollback. Do not
	// automatically resurrect removed users after a failed revocation deployment.
	_, _ = r.Run(ctx, n, "systemctl stop "+shellQuote(d.ServiceNameOrDefault()))
	tail, _ := r.Run(ctx, n, "journalctl -u "+shellQuote(d.ServiceNameOrDefault())+" -n 20 --no-pager 2>&1")
	return fmt.Errorf("sing-box failed to start; service stopped; previous configuration at %s: %v: %s", backup, startErr, strings.TrimSpace(tail))
}

// StageSingBoxConfig may refresh the validated candidate after final accounting
// removes a user who crossed quota during that sample.
func StageSingBoxConfig(ctx context.Context, r Runner, n model.Node, d model.CoreDeployment, cfg []byte) error {
	staged := d.ConfigPathOrDefault() + ".next"
	if err := PutFile(ctx, r, n, staged, cfg); err != nil {
		return err
	}
	if out, err := r.Run(ctx, n, singBoxBin+`"$SING_BOX_BIN" check -c `+shellQuote(staged)); err != nil {
		return fmt.Errorf("sing-box configuration validation failed: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

func CoreUnit(ctx context.Context, r Runner, n model.Node, d model.CoreDeployment, action string) error {
	switch action {
	case "start", "stop", "restart":
	default:
		return fmt.Errorf("invalid core action %q", action)
	}
	_, err := r.Run(ctx, n, "systemctl "+action+" "+shellQuote(d.ServiceNameOrDefault()))
	return err
}
func CoreIsActive(ctx context.Context, r Runner, n model.Node, d model.CoreDeployment) (bool, error) {
	out, err := r.Run(ctx, n, "systemctl is-active "+shellQuote(d.ServiceNameOrDefault())+" || true")
	return strings.TrimSpace(out) == "active", err
}

// CoreGeneration changes on every systemd start, including an operator restart.
func CoreGeneration(ctx context.Context, r Runner, n model.Node, d model.CoreDeployment) (string, error) {
	out, err := r.Run(ctx, n, "systemctl show -p InvocationID --value "+shellQuote(d.ServiceNameOrDefault()))
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(out)
	if len(id) != 32 || strings.Trim(id, "0") == "" {
		return "", fmt.Errorf("sing-box service has no active InvocationID: %q", id)
	}
	return id, nil
}

func SingBoxVersion(ctx context.Context, r Runner, n model.Node) (string, error) {
	out, err := r.Run(ctx, n, singBoxBin+`"$SING_BOX_BIN" version`)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Fields(line)
		if len(parts) >= 3 && parts[0] == "sing-box" && parts[1] == "version" {
			return parts[2], nil
		}
	}
	return "", fmt.Errorf("unrecognized sing-box version: %s", out)
}
