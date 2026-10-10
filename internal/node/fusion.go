package node

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/fusion"
	"github.com/RovenEverward/my-proxy/internal/model"
)

const FusionBinPath = "/usr/local/lib/mpx/goecs/" + fusion.Version + "/goecs"
const fusionCaptureLimit = (2 << 20) + 1024

type fusionBoundedRunner interface {
	RunBounded(context.Context, model.Node, string, []byte, int) (string, error)
}

// RunFusion executes only the checksum-verified official binary. Every transport
// capture is bounded; a Runner without that capability fails before remote work.
func RunFusion(ctx context.Context, r Runner, n model.Node, cfg fusion.Config, cacheDir string) (report fusion.Report, err error) {
	cfg, err = cfg.Normalized()
	report = fusion.NewReport(cfg)
	defer func() {
		if errors.Is(err, context.Canceled) {
			report.Status = "canceled"
		}
		if errors.Is(err, context.DeadlineExceeded) {
			report.Status = "timeout"
		}
		report.Finish(err)
	}()
	if err != nil {
		return
	}
	bounded, ok := r.(fusionBoundedRunner)
	if !ok {
		err = fmt.Errorf("fusion requires a bounded node runner")
		return
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(cfg.TimeoutSeconds)*time.Second)
	defer func() {
		if ctx.Err() != nil {
			err = errors.Join(err, ctx.Err())
		}
		cancel()
	}()
	arch, probeErr := bounded.RunBounded(ctx, n, `test "$(uname -s)" = Linux || exit 1; uname -m`, nil, 1024)
	if probeErr != nil {
		err = fmt.Errorf("fusion platform probe: %w", probeErr)
		return
	}
	switch strings.TrimSpace(arch) {
	case "x86_64":
		arch = "amd64"
	case "aarch64", "arm64":
		arch = "arm64"
	default:
		err = fmt.Errorf("unsupported fusion Linux architecture %q", strings.TrimSpace(arch))
		return
	}
	exe, sum, loadErr := fusion.LoadExecutable(ctx, arch, cacheDir)
	if loadErr != nil {
		err = loadErr
		return
	}
	cached, probeErr := bounded.RunBounded(ctx, n, fusionCachedScript(sum), nil, 1024)
	if probeErr != nil || strings.TrimSpace(cached) != "verified" {
		var packed bytes.Buffer
		writer := gzip.NewWriter(&packed)
		if _, err = writer.Write(exe); err != nil {
			return
		}
		if err = writer.Close(); err != nil {
			return
		}
		input := []byte(base64.StdEncoding.EncodeToString(packed.Bytes()))
		if _, err = bounded.RunBounded(ctx, n, fusionInstallScript(sum), input, 16<<10); err != nil {
			err = fmt.Errorf("fusion official binary install: %w", err)
			return
		}
	}
	started := report.StartedAt
	report, err = runFusionStages(ctx, bounded, n, cfg)
	report.StartedAt = started
	return
}

type fusionPhase string

const (
	fusionBaseline fusionPhase = "baseline"
	fusionSpeed    fusionPhase = "speed"
)

func fusionRemaining(ctx context.Context) int {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0
	}
	return max(0, int(time.Until(deadline).Seconds()))
}

func runFusionStages(ctx context.Context, r fusionBoundedRunner, n model.Node, cfg fusion.Config) (fusion.Report, error) {
	wall := fusionRemaining(ctx)
	if wall < 1 {
		report := fusion.NewReport(cfg)
		report.Status = "timeout"
		return report, context.DeadlineExceeded
	}
	baseWall := wall
	if cfg.Speed {
		capSeconds := 180
		if cfg.Hardware {
			capSeconds = 240
		}
		baseWall = min(capSeconds, wall)
	}
	baseEngine := baseWall
	if cfg.Speed {
		baseEngine = max(1, baseWall-min(30, baseWall/4))
	}
	baseCtx, baseCancel := context.WithTimeout(ctx, time.Duration(baseWall)*time.Second)
	base, raw, baseErr := fusionRunPhase(baseCtx, r, n, cfg, fusionBaseline, baseEngine, baseWall)
	baseCancel()
	base.Speed = cfg.Speed
	if !cfg.Speed {
		return base, baseErr
	}
	if len(raw) == 0 || baseErr != nil || !fusionHasComponents(raw) {
		return base, baseErr
	}
	stages := []fusion.StageRecord{fusion.RecordStage("baseline", base, raw)}
	remaining := fusionRemaining(ctx)
	if remaining < 10 || ctx.Err() != nil {
		speed := fusion.NewReport(cfg)
		speed.Status = "partial"
		speed.Error = "speed stage skipped: insufficient remaining overall budget"
		phaseErr := error(nil)
		if ctx.Err() != nil {
			phaseErr = ctx.Err()
			speed.Error = phaseErr.Error()
			speed.Status = "canceled"
			if errors.Is(phaseErr, context.DeadlineExceeded) {
				speed.Status = "timeout"
			}
		}
		speed.Finish(phaseErr)
		stages = append(stages, fusion.StageRecord{Name: "speed", Attempted: false, Status: "skipped", Error: speed.Error})
		return fusion.MergeSpeed(base, raw, speed, nil, stages), phaseErr
	}
	// Keep a hard reserve for independent samples even if the upstream process
	// ignores its own soft timeout. The outer Fusion deadline never grows.
	reserve := min(50, remaining/4)
	speedWall := max(1, remaining-reserve)
	engine := max(1, speedWall-min(10, speedWall/4))
	speedCtx, speedCancel := context.WithTimeout(ctx, time.Duration(speedWall)*time.Second)
	speed, speedRaw, speedErr := fusionRunPhase(speedCtx, r, n, cfg, fusionSpeed, engine, speedWall)
	speedCancel()
	stages = append(stages, fusion.RecordStage("speed", speed, speedRaw))
	return fusion.MergeSpeed(base, raw, speed, speedRaw, stages), speedErr
}

func fusionHasComponents(data []byte) bool {
	var report struct {
		Components []json.RawMessage `json:"components"`
	}
	return json.Unmarshal(data, &report) == nil && len(report.Components) > 0
}

func fusionRunPhase(ctx context.Context, r fusionBoundedRunner, n model.Node, cfg fusion.Config, phase fusionPhase, engineSeconds, wallSeconds int) (report fusion.Report, raw json.RawMessage, err error) {
	report = fusion.NewReport(cfg)
	defer func() { report.Finish(err) }()
	token := make([]byte, 16)
	if _, err = rand.Read(token); err != nil {
		return
	}
	runDir := "/tmp/mpx-fusion-" + hex.EncodeToString(token)
	out, runErr := r.RunBounded(ctx, n, fusionPhaseScript(cfg, phase, runDir, engineSeconds, wallSeconds), nil, fusionCaptureLimit)
	// Recover from transport cancellation with a separate, tightly bounded SSH
	// cleanup. If the connection is gone, the remote trap/watchdog still owns it.
	if ctx.Err() != nil || errors.Is(runErr, ErrOutputLimit) {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		recovered, cleanupErr := r.RunBounded(cleanupCtx, n, fusionRecoverScript(runDir), nil, fusionCaptureLimit)
		cleanupCancel()
		if len(recovered) > len(out) {
			out = recovered
		}
		if cleanupErr != nil {
			report.Warnings = append(report.Warnings, "remote cleanup recovery failed; watchdog remains armed")
		}
	}
	timedOut := strings.HasPrefix(out, "MPX_FUSION_TIMEOUT\n")
	out = strings.TrimPrefix(out, "MPX_FUSION_TIMEOUT\n")
	overflow := strings.HasPrefix(out, "MPX_FUSION_OVERFLOW\n")
	out = strings.TrimPrefix(out, "MPX_FUSION_OVERFLOW\n")
	raw = fusion.OfficialData(out)
	report.Capture(out)
	if overflow {
		report.Truncated = true
		report.Status = "partial"
		report.Warnings = append(report.Warnings, "official output exceeded 2 MiB capture limit")
	}
	if timedOut {
		err = context.DeadlineExceeded
		report.Status = "timeout"
	}
	if ctx.Err() != nil {
		err = ctx.Err()
		report.Status = "canceled"
		if errors.Is(err, context.DeadlineExceeded) {
			report.Status = "timeout"
		}
	} else if runErr != nil && !timedOut {
		err = runErr
		if report.Output != "" || len(report.Data) > 0 {
			report.Status = "partial"
		} else {
			report.Status = "failed"
		}
		if errors.Is(err, ErrOutputLimit) {
			report.Truncated = true
		}
	}
	if err == nil && report.Error != "" {
		err = errors.New(report.Error)
	}
	if err == nil && (report.Status == "failed" || report.Status == "timeout" || report.Status == "canceled") {
		err = fmt.Errorf("official fusion report status: %s", report.Status)
	}
	return
}

func fusionCachedScript(sum string) string {
	return "bash -c " + shellQuote(`set -eu
bin=`+shellQuote(FusionBinPath)+`
dir=${bin%/*}
for path in /usr /usr/local /usr/local/lib /usr/local/lib/mpx /usr/local/lib/mpx/goecs "$dir" "$bin" "$dir/LICENSE.goecs" "$dir/NOTICE.goecs"; do [ ! -L "$path" ] || { echo "unsafe fusion path" >&2; exit 1; }; done
[ ! -L "$dir" ] && [ ! -L "$bin" ] && [ -f "$bin" ] && [ -f "$dir/LICENSE.goecs" ] && [ -f "$dir/NOTICE.goecs" ] || exit 1
printf '%s  %s\n' `+shellQuote(sum)+` "$bin" | sha256sum -c - >/dev/null
timeout -k 2 15 "$bin" -h > /dev/null
printf verified
`)
}

func fusionInstallScript(sum string) string {
	return "bash -c " + shellQuote(`set -eu
umask 077
for tool in bash setsid timeout sha256sum gzip base64 head tail wc tee cat grep readlink mkfifo; do command -v "$tool" >/dev/null || { echo "missing required utility: $tool" >&2; exit 1; }; done
bin=`+shellQuote(FusionBinPath)+`
dir=${bin%/*}
for path in /usr /usr/local /usr/local/lib /usr/local/lib/mpx /usr/local/lib/mpx/goecs "$dir" "$bin" "$dir/LICENSE.goecs" "$dir/NOTICE.goecs"; do [ ! -L "$path" ] || { echo "unsafe fusion path" >&2; exit 1; }; done
[ ! -L "$dir" ] && [ ! -L "$bin" ] || { echo 'unsafe fusion install path' >&2; exit 1; }
mkdir -p "$dir"
tmp=$(mktemp "$dir/.goecs.XXXXXXXX")
trap 'rm -f -- "$tmp"' EXIT HUP INT TERM
# Bound both compressed transfer and decompression before allocating disk space.
ulimit -f 49152
base64 -d | gzip -dc > "$tmp"
printf '%s  %s\n' `+shellQuote(sum)+` "$tmp" | sha256sum -c - >/dev/null
chmod 700 "$tmp"
timeout -k 2 15 "$tmp" -h > /dev/null
[ "$(timeout -k 2 15 "$tmp" -v)" = `+shellQuote(fusion.Version)+` ] || { echo 'fusion executable version mismatch' >&2; exit 1; }
mv -f -- "$tmp" "$bin"
printf '%s' `+shellQuote(string(fusion.License))+` > "$dir/LICENSE.goecs"
printf '%s' `+shellQuote(fusion.Notice)+` > "$dir/NOTICE.goecs"
`)
}

func fusionArguments(cfg fusion.Config, seconds int) string {
	return fusionPhaseArguments(cfg, fusionBaseline, seconds)
}

func fusionPhaseArguments(cfg fusion.Config, phase fusionPhase, seconds int) string {
	speed := phase == fusionSpeed
	hardware := cfg.Hardware && !speed
	core := !speed
	routes := cfg.Speed && !speed
	return fmt.Sprintf("-menu=false -l=zh -json=- -privacy=true -upload=false -log=false -timeout=%ds -hardware-budget=%ds -cpu=%t -memory=%t -disk=%t -diskmc=false -speed=%t -spnum=1 -ut=%t -utregion=20 -utipver=auto -ut-concurrency=20 -basic=%t -security=%t -email=%t -backtrace=%t -nt3=%t -nt3loc=GZ -nt3t=ipv4 -ping=%t -ping-scope=auto -tgdc=false -web=false -tcp=false", seconds, max(1, min(seconds, 120)), hardware, hardware, hardware, speed, core, core, core, core, core, routes, core)
}

// The session leader includes the binary and every descendant. Auxiliary
// watchdogs use their own sessions so cleanup cannot leave sleeping children.
const fusionTreeCleanup = `
snapshot_tree() {
 local parent=$1 file child
 case "$parent" in ''|*[!0-9]*) return;; esac
 [ "$parent" -gt 1 ] || return
 for file in /proc/"$parent"/task/*/children; do
  [ -r "$file" ] || continue
  for child in $(cat "$file" 2>/dev/null); do
   case " $owned " in *" $child "*) continue;; esac
   owned="$owned $child"
   snapshot_tree "$child"
  done
 done
}
stop_owned() {
 local root=$1 child
 owned=
 snapshot_tree "$root"
 owned="$owned $root"
 for child in $owned; do kill -TERM -- "-$child" 2>/dev/null || :; kill -TERM "$child" 2>/dev/null || :; done
 sleep 1
 for child in $owned; do kill -KILL -- "-$child" 2>/dev/null || :; kill -KILL "$child" 2>/dev/null || :; done
}
`

func fusionRunScript(cfg fusion.Config, runDir string, seconds int) string {
	return fusionPhaseScript(cfg, fusionBaseline, runDir, seconds, seconds)
}

func fusionPhaseScript(cfg fusion.Config, phase fusionPhase, runDir string, engineSeconds, wallSeconds int) string {
	payload := `set -eu
` + fusionTreeCleanup + `
cd ` + shellQuote(runDir) + `
mkfifo stream
{ head -c 2097152 > output; if [ "$(wc -c < output)" -eq 2097152 ]; then touch overflow; fi; cat > /dev/null; } < stream &
drainer=$!
engine=
clean_engine() {
 trap - EXIT INT TERM HUP
 if [ -n "$engine" ]; then stop_owned "$engine"; wait "$engine" 2>/dev/null || :; fi
 if [ -n "$drainer" ]; then kill "$drainer" 2>/dev/null || :; wait "$drainer" 2>/dev/null || :; fi
}
trap clean_engine EXIT
trap 'exit 143' INT TERM HUP
` + shellQuote(FusionBinPath) + ` ` + fusionPhaseArguments(cfg, phase, engineSeconds) + ` > stream 2>&1 &
engine=$!
printf '%s\n' "$engine" > engine
set +e
wait "$engine"
rc=$?
engine=
wait "$drainer"
drainer=
exit "$rc"
`
	script := `set -eu
` + fusionTreeCleanup + `
umask 077
d=` + shellQuote(runDir) + `
case "$d" in /tmp/mpx-fusion-????????????????????????????????) ;; *) exit 1;; esac
mkdir -m 700 "$d"
printf '%s\n' "$$" > "$d/owner"
group=; timer=; monitor=
cleanup() {
 rc=$?
 trap - EXIT HUP INT TERM
 if [ -n "$group" ]; then stop_owned "$group"; wait "$group" 2>/dev/null || :; fi
 for pid in "$timer" "$monitor"; do if [ -n "$pid" ]; then kill -TERM -- "-$pid" 2>/dev/null || :; kill -KILL -- "-$pid" 2>/dev/null || :; wait "$pid" 2>/dev/null || :; fi; done
 if [ -f "$d/timeout" ]; then printf 'MPX_FUSION_TIMEOUT\n'; fi
 if [ -f "$d/overflow" ]; then printf 'MPX_FUSION_OVERFLOW\n'; fi
 if [ -f "$d/output" ]; then head -c 2097152 "$d/output" || :; fi
 rm -rf -- "$d"
 exit "$rc"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP
setsid bash -c ` + shellQuote(payload) + ` < /dev/null &
group=$!
printf '%s\n' "$group" > "$d/pgid"
setsid bash -c 'sleep "$1"; touch "$3/timeout"; kill -TERM "$2"' _ ` + strconv.Itoa(wallSeconds) + ` "$$" "$d" < /dev/null > /dev/null 2>&1 &
timer=$!
setsid bash -c 'while kill -0 "$1" 2>/dev/null; do sleep 1; done; kill -TERM "$2"' _ "$PPID" "$$" < /dev/null > /dev/null 2>&1 &
monitor=$!
set +e
wait "$group"
rc=$?
exit "$rc"
`
	return "bash -c " + shellQuote(script)
}

func fusionRecoverScript(runDir string) string {
	return "bash -c " + shellQuote(`set -eu
`+fusionTreeCleanup+`
d=`+shellQuote(runDir)+`
case "$d" in /tmp/mpx-fusion-????????????????????????????????) ;; *) exit 1;; esac
[ ! -L "$d" ] || exit 1
if [ ! -d "$d" ]; then exit 0; fi
if [ -f "$d/pgid" ] && [ ! -L "$d/pgid" ]; then
 pid=$(head -c 16 "$d/pgid")
 case "$pid" in ''|*[!0-9]*) exit 1;; esac
 [ "$pid" -gt 1 ] || exit 1
 if [ -d "/proc/$pid" ]; then
  [ "$(readlink "/proc/$pid/cwd")" = "$d" ] || exit 1
  grep -Fq -- "$d" "/proc/$pid/cmdline" || exit 1
  stop_owned "$pid"
 fi
fi
if [ -f "$d/timeout" ]; then printf 'MPX_FUSION_TIMEOUT\n'; fi
if [ -f "$d/overflow" ]; then printf 'MPX_FUSION_OVERFLOW\n'; fi
if [ -f "$d/output" ] && [ ! -L "$d/output" ]; then head -c 2097152 "$d/output" || :; fi
if [ -f "$d/owner" ]; then
 owner=$(head -c 16 "$d/owner")
 case "$owner" in ''|*[!0-9]*) exit 1;; esac
 if [ -d "/proc/$owner" ] && grep -Fq -- "$d" "/proc/$owner/cmdline"; then
  kill -TERM "$owner" 2>/dev/null || :
  for attempt in {1..50}; do
   if ! grep -Fq -- "$d" "/proc/$owner/cmdline" 2>/dev/null; then break; fi
   sleep 0.1
  done
  if grep -Fq -- "$d" "/proc/$owner/cmdline" 2>/dev/null; then stop_owned "$owner"; fi
 fi
fi
rm -rf -- "$d"
`)
}
