package node

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/fusion"
	"github.com/RovenEverward/my-proxy/internal/model"
)

func TestFusionRequiresBoundedRunner(t *testing.T) {
	r, err := RunFusion(t.Context(), &recordRunner{}, model.Node{}, fusion.Config{}, t.TempDir())
	if err == nil || r.Status != "failed" {
		t.Fatalf("unbounded runner allowed: %+v %v", r, err)
	}
}

func TestFusionScriptGuards(t *testing.T) {
	args := fusionArguments(fusion.Config{}, 60)
	for _, want := range []string{"-upload=false", "-privacy=true", "-menu=false", "-json=-", "-cpu=false", "-disk=false", "-memory=false", "-speed=false", "-nt3=false"} {
		if !strings.Contains(args, want) {
			t.Fatal(want)
		}
	}
	script := fusionRunScript(fusion.Config{}, "/tmp/mpx-fusion-0123456789abcdef0123456789abcdef", 60)
	for _, want := range []string{"setsid", "kill -KILL", "wait \"$group\"", "trap cleanup EXIT", "head -c 2097152", "rm -rf -- \"$d\"", "kill -0"} {
		if !strings.Contains(script, want) {
			t.Fatal(want)
		}
	}
	install := fusionInstallScript(strings.Repeat("a", 64))
	for _, want := range []string{"sha256sum -c", "timeout -k 2 15", "base64 -d | gzip -dc", "mv -f", "LICENSE.goecs", "NOTICE.goecs"} {
		if !strings.Contains(install, want) {
			t.Fatal(want)
		}
	}
	if strings.Contains(install, "apt-get") || strings.Contains(install, "curl ") {
		t.Fatal("installer escaped pinned transfer")
	}
	for _, s := range []string{script, install, fusionRecoverScript("/tmp/mpx-fusion-0123456789abcdef0123456789abcdef")} {
		cmd := exec.Command("bash", "-n", "-c", s)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("script syntax: %v %s", err, out)
		}
	}
}

type fusionDockerRunner struct{ container string }

func (r fusionDockerRunner) Run(context.Context, model.Node, string) (string, error) {
	panic("unbounded")
}
func (r fusionDockerRunner) RunWithInput(context.Context, model.Node, string, []byte) (string, error) {
	panic("unbounded")
}
func (r fusionDockerRunner) RunBounded(ctx context.Context, _ model.Node, script string, input []byte, limit int) (string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", r.container, "bash", "-c", script)
	cmd.Stdin = bytes.NewReader(input)
	out := &boundedCapture{limit: limit, cancel: cancel}
	cmd.Stdout = out
	cmd.Stderr = out
	err := cmd.Run()
	if out.exceeded {
		return out.String(), ErrOutputLimit
	}
	return out.String(), err
}

// This opt-in proof executes official assets in a disposable Linux Docker
// container, never against configured nodes. MPX_FUSION_TEST_ASSETS points to a
// directory containing the pinned goecs_linux_amd64.zip and arm64.zip archives.
func TestFusionOfficialDocker(t *testing.T) {
	assets := os.Getenv("MPX_FUSION_TEST_ASSETS")
	if assets == "" {
		t.Skip("set MPX_FUSION_TEST_ASSETS for isolated official runtime proof")
	}
	name := fmt.Sprintf("mpx-fusion-fixture-%d", time.Now().UnixNano())
	image := os.Getenv("MPX_FUSION_TEST_IMAGE")
	if image == "" {
		image = "ubuntu:22.04"
	}
	if out, err := exec.Command("docker", "run", "--name", name, "--init", "-d", image, "sleep", "900").CombinedOutput(); err != nil {
		t.Fatalf("docker create: %v %s", err, out)
	}
	t.Cleanup(func() { // Exact generated container name, not a user-owned container.
		out, err := exec.Command("docker", "rm", "-f", name).CombinedOutput()
		if err != nil {
			t.Errorf("docker cleanup: %v %s", err, out)
		}
	})
	runner := fusionDockerRunner{name}
	machine, err := runner.RunBounded(t.Context(), model.Node{}, "uname -m", nil, 1024)
	if err != nil {
		t.Fatal(err)
	}
	arch := "amd64"
	if strings.TrimSpace(machine) == "aarch64" {
		arch = "arm64"
	}
	cache := t.TempDir()
	archive, err := os.ReadFile(filepath.Join(assets, "goecs_linux_"+arch+".zip"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(cache, "goecs-"+fusion.Version+"-linux-"+arch+".zip"), archive, 0600); err != nil {
		t.Fatal(err)
	}
	// Exercise installation then cached reuse with cancellation during actual
	// network startup, where upstream startup can precede its own deadline.
	for pass := 0; pass < 2; pass++ {
		ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
		report, err := RunFusion(ctx, runner, model.Node{}, fusion.Config{TimeoutSeconds: 60}, cache)
		cancel()
		t.Logf("pass%d status=%s err=%v output=%d data=%d warnings=%v", pass, report.Status, err, len(report.Output), len(report.Data), report.Warnings)
		if err == nil || report.Status != "timeout" {
			t.Fatalf("expected owned timeout: %+v %v", report, err)
		}
		out, err := runner.RunBounded(t.Context(), model.Node{}, `find /tmp -maxdepth 1 -name 'mpx-fusion-*'; ps -eo pid,args | grep -E '[g]oecs|[s]leep 60|[h]ead -c 2097152|[t]ee /dev/fd'`, nil, 4096)
		if strings.TrimSpace(out) != "" {
			t.Fatalf("left runtime artifacts: %s (%v)", out, err)
		}
	}
	ctxFull, cancelFull := context.WithTimeout(t.Context(), 75*time.Second)
	full, fullErr := RunFusion(ctxFull, runner, model.Node{}, fusion.Config{TimeoutSeconds: 60}, cache)
	cancelFull()
	t.Logf("full default official adapter status=%s err=%v output=%d data=%d warnings=%v", full.Status, fullErr, len(full.Output), len(full.Data), full.Warnings)
	if full.Status == "done" && fullErr != nil {
		t.Fatalf("false completed report: %+v %v", full, fullErr)
	}
	// Minimal official report fixture disables remote test modules; DNS startup
	// still exists upstream, so the wrapper is bounded by a separate host timeout.
	flags := "-menu=false -l=zh -json=- -privacy=true -upload=false -log=false -data-offline=true -dns-mode=system -basic=false -security=false -email=false -ut=false -backtrace=false -nt3=false -speed=false -ping=false -tgdc=false -web=false -tcp=false -cpu=false -memory=false -disk=false -timeout=20s"
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	out, err := runner.RunBounded(ctx, model.Node{}, "timeout -k 2 40 "+FusionBinPath+" "+flags, nil, 256<<10)
	t.Logf("minimal official command: %s %s; err=%v bytes=%d", FusionBinPath, flags, err, len(out))
	if err != nil {
		t.Fatalf("minimal official fixture: %v %s", err, out)
	}
	report := fusion.NewReport(fusion.Config{})
	report.Capture(out)
	if len(report.Data) == 0 || !json.Valid(report.Data) {
		t.Fatalf("official fixture schema missing: %+v", report)
	}
	t.Logf("minimal report status=%s warnings=%v", report.Status, report.Warnings)
}

type fusionSetupCanceledRunner struct{ fusionDockerRunner }

func (fusionSetupCanceledRunner) RunBounded(ctx context.Context, _ model.Node, _ string, _ []byte, _ int) (string, error) {
	<-ctx.Done()
	return "", fmt.Errorf("ssh connection closed")
}
func TestFusionSetupCancellationStatus(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		expected := "canceled"
		if deadline {
			ctx, cancel = context.WithTimeout(t.Context(), time.Millisecond)
			expected = "timeout"
		} else {
			cancel()
		}
		r, err := RunFusion(ctx, fusionSetupCanceledRunner{}, model.Node{}, fusion.Config{}, t.TempDir())
		cancel()
		if err == nil || r.Status != expected || !strings.Contains(r.Error, "ssh connection closed") {
			t.Fatalf("lost setup cancellation: %+v %v", r, err)
		}
	}
}
