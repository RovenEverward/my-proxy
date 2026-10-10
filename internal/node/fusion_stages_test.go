package node

import (
	"context"
	"encoding/json"
	"errors"
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

func stagedJSON(status string, speed bool) string {
	name, schema := "basics", "goecs.basics/system-v1"
	section := "basics"
	if speed {
		name, schema, section = "speed.registry", "goecs.speed/v1", "speed"
	}
	raw, _ := json.MarshalIndent(struct {
		Schema     string `json:"schema_version"`
		Version    string `json:"ecs_version"`
		Status     string `json:"status"`
		Components []any  `json:"components"`
		Sections   []any  `json:"sections"`
		Text       string `json:"text"`
	}{"goecs.report/v1", fusion.Version, status, []any{map[string]any{"name": name, "schema_version": schema, "status": "ok", "payload": map[string]any{"schema_version": schema, "evidence": "retained"}}}, []any{map[string]any{"name": section, "enabled": true, "status": "ok"}}, "official baseline text"}, "", "  ")
	return string(raw)
}

type stagedRunner struct {
	t           *testing.T
	calls       int
	deadlines   []time.Time
	cancel      context.CancelFunc
	broken      bool
	speedOutput string
}

func (r *stagedRunner) RunBounded(ctx context.Context, _ model.Node, script string, _ []byte, limit int) (string, error) {
	if limit != fusionCaptureLimit {
		r.t.Fatalf("unbounded phase %d", limit)
	}
	if strings.Contains(script, "snapshot_tree") && !strings.Contains(script, "mkfifo stream") {
		return "", nil
	}
	deadline, _ := ctx.Deadline()
	r.deadlines = append(r.deadlines, deadline)
	r.calls++
	if r.calls == 1 {
		if strings.Contains(script, "-speed=true") || !strings.Contains(script, "-nt3=true") {
			r.t.Fatal("baseline incorrectly selected")
		}
		if r.broken {
			return "truncated report", nil
		}
		return stagedJSON("ok", false), nil
	}
	if !strings.Contains(script, "-speed=true") || !strings.Contains(script, "-ut=false") || !strings.Contains(script, "-cpu=false") || strings.Contains(script, "-nt3=true") {
		r.t.Fatal("speed phase repeats core")
	}
	if r.cancel != nil {
		r.cancel()
		return "partial speed text", ctx.Err()
	}
	return r.speedOutput, nil
}

func TestFusionStagedDeadlineAndCancellation(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(t.Context(), 900*time.Second)
		r := &stagedRunner{t: t, speedOutput: stagedJSON("ok", true)}
		if canceled {
			r.cancel = cancel
		}
		outer, _ := ctx.Deadline()
		got, err := runFusionStages(ctx, r, model.Node{}, fusion.Config{Speed: true, TimeoutSeconds: 900})
		cancel()
		if r.calls != 2 || len(r.deadlines) != 2 || r.deadlines[0].After(time.Now().Add(181*time.Second)) || r.deadlines[1].After(outer) {
			t.Fatalf("inflated budget: %+v", r.deadlines)
		}
		if !strings.Contains(string(got.Data), "goecs.basics/system-v1") || !got.Speed || !strings.HasPrefix(got.Output, "official baseline text") {
			t.Fatalf("baseline lost: %+v", got)
		}
		if canceled {
			if !errors.Is(err, context.Canceled) || got.Status != "canceled" {
				t.Fatalf("cancel lost: %+v %v", got, err)
			}
		} else if err != nil || got.Status != "done" {
			t.Fatalf("success: %+v %v", got, err)
		}
	}
}
func TestFusionInvalidBaselineSkipsSpeed(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 900*time.Second)
	defer cancel()
	r := &stagedRunner{t: t, broken: true}
	report, err := runFusionStages(ctx, r, model.Node{}, fusion.Config{Speed: true, TimeoutSeconds: 900})
	if r.calls != 1 || err == nil || report.Status != "partial" {
		t.Fatalf("invalid baseline continued: %+v %v calls%d", report, err, r.calls)
	}
}
func TestFusionPhaseFlags(t *testing.T) {
	base := fusionPhaseArguments(fusion.Config{Speed: true, Hardware: true}, fusionBaseline, 150)
	speed := fusionPhaseArguments(fusion.Config{Speed: true, Hardware: true}, fusionSpeed, 60)
	for _, flag := range []string{"-speed=false", "-nt3=true", "-cpu=true", "-ut=true"} {
		if !strings.Contains(base, flag) {
			t.Fatal(base)
		}
	}
	for _, flag := range []string{"-speed=true", "-basic=false", "-security=false", "-email=false", "-backtrace=false", "-nt3=false", "-ping=false", "-cpu=false", "-privacy=true", "-upload=false"} {
		if !strings.Contains(speed, flag) {
			t.Fatal(speed)
		}
	}
	for _, phase := range []fusionPhase{fusionBaseline, fusionSpeed} {
		script := fusionPhaseScript(fusion.Config{Speed: true}, phase, "/tmp/mpx-fusion-0123456789abcdef0123456789abcdef", 60, 90)
		if out, err := exec.Command("bash", "-n", "-c", script).CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
	}
}

// The official speed invocation starts unchanged; this runner cancels its host
// context after eight seconds to verify preservation without a full traffic run.
type stagedCancelDocker struct {
	fusionDockerRunner
	speedStarted bool
	cancel       context.CancelFunc
}

func (r *stagedCancelDocker) RunBounded(ctx context.Context, n model.Node, script string, input []byte, limit int) (string, error) {
	if strings.Contains(script, "mkfifo stream") && strings.Contains(script, "-speed=true") {
		r.speedStarted = true
		timer := time.AfterFunc(8*time.Second, r.cancel)
		defer timer.Stop()
		return r.fusionDockerRunner.RunBounded(ctx, n, script, input, limit)
	}
	return r.fusionDockerRunner.RunBounded(ctx, n, script, input, limit)
}
func TestFusionOfficialStagesDocker(t *testing.T) {
	assets := os.Getenv("MPX_FUSION_TEST_ASSETS")
	if assets == "" {
		t.Skip("set MPX_FUSION_TEST_ASSETS for official staged runtime")
	}
	name := fmt.Sprintf("mpx-fusion-stages-%d", time.Now().UnixNano())
	if out, err := exec.Command("docker", "run", "--init", "--name", name, "-d", "ubuntu:22.04", "sleep", "180").CombinedOutput(); err != nil {
		t.Fatalf("docker create %v %s", err, out)
	}
	t.Cleanup(func() {
		if out, err := exec.Command("docker", "rm", "-f", name).CombinedOutput(); err != nil {
			t.Errorf("cleanup %v %s", err, out)
		}
	})
	runner := &stagedCancelDocker{fusionDockerRunner: fusionDockerRunner{name}}
	archout, err := runner.fusionDockerRunner.RunBounded(t.Context(), model.Node{}, "uname -m", nil, 1024)
	if err != nil {
		t.Fatal(err)
	}
	arch := "amd64"
	if strings.TrimSpace(archout) == "aarch64" {
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
	started := time.Now()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	runner.cancel = cancel
	report, runErr := RunFusion(ctx, runner, model.Node{}, fusion.Config{Speed: true, TimeoutSeconds: 60}, cache)
	t.Logf("official Speedtrue60: elapsed=%s status=%s error=%v output=%d data=%d speedStarted=%t warnings=%v", time.Since(started), report.Status, runErr, len(report.Output), len(report.Data), runner.speedStarted, report.Warnings)
	if !runner.speedStarted || !strings.Contains(string(report.Data), "unlocktests.media") || !strings.Contains(string(report.Data), "adapter_stages") || !report.Speed {
		t.Fatalf("stages did not retain core: %+v %v", report, runErr)
	}
	if runErr == nil || report.Status == "done" {
		t.Fatalf("speed cancellation missing: %+v", report)
	}
	if time.Since(started) > 75*time.Second {
		t.Fatal("stages inflated 60-second overall deadline plus cleanup")
	}
	// A canceled docker exec transport can leave the process behind unless recovery
	// receives its own context; check the actual descendants and temp paths.
	out, _ := runner.fusionDockerRunner.RunBounded(t.Context(), model.Node{}, `find /tmp -maxdepth 1 -name 'mpx-fusion-*'; ps -eo pid,args | grep -E '[g]oecs|[h]ead -c 2097152'`, nil, 4096)
	if strings.TrimSpace(out) != "" {
		t.Fatalf("leaked staged process/temp: %s", out)
	}
	// Exercise natural official soft/hard deadlines with no injected cancellation.
	started = time.Now()
	natural, naturalErr := RunFusion(t.Context(), runner.fusionDockerRunner, model.Node{}, fusion.Config{Speed: true, TimeoutSeconds: 60}, cache)
	t.Logf("official natural Speedtrue60: elapsed=%s status=%s error=%v output=%d data=%d warnings=%v", time.Since(started), natural.Status, naturalErr, len(natural.Output), len(natural.Data), natural.Warnings)
	if !strings.Contains(string(natural.Data), "unlocktests.media") || !strings.Contains(string(natural.Data), "adapter_stages") {
		t.Fatalf("natural speed deadline discarded baseline: %+v %v", natural, naturalErr)
	}
	if time.Since(started) > 75*time.Second {
		t.Fatal("natural stages inflated overall60 deadline")
	}
	out, _ = runner.fusionDockerRunner.RunBounded(t.Context(), model.Node{}, `find /tmp -maxdepth 1 -name 'mpx-fusion-*'; ps -eo pid,args | grep -E '[g]oecs|[h]ead -c 2097152'`, nil, 4096)
	if strings.TrimSpace(out) != "" {
		t.Fatalf("natural stage leaked process/temp: %s", out)
	}
}

func TestFusionInsufficientBudgetRecordsNoSpeedInvocation(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 9*time.Second)
	defer cancel()
	r := &stagedRunner{t: t}
	got, err := runFusionStages(ctx, r, model.Node{}, fusion.Config{Speed: true, TimeoutSeconds: 60})
	if r.calls != 1 || err != nil {
		t.Fatalf("unexpected invocation count/error: %d %v", r.calls, err)
	}
	var root struct {
		Stages []fusion.StageRecord `json:"adapter_stages"`
	}
	_ = json.Unmarshal(got.Data, &root)
	if len(root.Stages) != 2 || !root.Stages[0].Attempted || root.Stages[1].Attempted || root.Stages[1].Status != "skipped" || !root.Stages[1].StartedAt.IsZero() {
		t.Fatalf("fictitious speedstage: %+v", root.Stages)
	}
}
