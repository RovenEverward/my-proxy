package singboxbuild_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/node"
)

type installRunner struct {
	calls       []string
	version     string
	existingSHA string
	failConfig  bool
	unit        string
}

func (r *installRunner) Run(_ context.Context, _ model.Node, script string) (string, error) {
	r.calls = append(r.calls, script)
	if strings.Contains(script, "sha256sum") && r.existingSHA != "" {
		return node.SingBoxBinPath + " " + r.existingSHA + "\n", nil
	}
	if strings.HasSuffix(script, " version") {
		return r.version, nil
	}
	if strings.Contains(script, " check -c ") && r.failConfig {
		return "bad configuration", fmt.Errorf("exit status 1")
	}
	return "", nil
}
func (r *installRunner) RunWithInput(ctx context.Context, n model.Node, script string, body []byte) (string, error) {
	if strings.Contains(script, "/etc/systemd/system/") {
		r.unit = string(body)
	}
	return r.Run(ctx, n, script)
}

func TestInstallRejectsMissingTagsBeforeReplacement(t *testing.T) {
	for _, tags := range []string{"with_quic", "with_v2ray_api", "with_quic,with_v2ray_api_extra", ""} {
		t.Run(tags, func(t *testing.T) {
			r := &installRunner{version: "sing-box version 1.14.3\nTags: " + tags + "\n"}
			err := node.InstallSingBox(t.Context(), r, model.Node{}, model.CoreDeployment{Core: model.CoreSingBox}, installBinary(t))
			if err == nil || !strings.Contains(err.Error(), "构建标签") {
				t.Fatalf("missing tag was accepted: %v", err)
			}
			if strings.Contains(strings.Join(r.calls, "\n"), "mv -f '") {
				t.Fatal("replaced binary after tag rejection")
			}
		})
	}
}

func TestInstallChecksConfigBeforeReplacementAndSkipsSameBinary(t *testing.T) {
	bin := installBinary(t)
	body, _ := os.ReadFile(bin)
	sum := fmt.Sprintf("%x", sha256.Sum256(body))
	for _, tc := range []struct {
		name          string
		same, invalid bool
	}{{"replace", false, false}, {"cache", true, false}, {"bad config", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			r := &installRunner{version: "sing-box version 1.14.3\nTags: with_quic,with_v2ray_api\n", failConfig: tc.invalid}
			if tc.same {
				r.existingSHA = sum
			}
			d := model.CoreDeployment{Core: model.CoreSingBox}
			err := node.InstallSingBox(t.Context(), r, model.Node{}, d, bin)
			calls := strings.Join(r.calls, "\n")
			replace := strings.Index(calls, "mv -f '")
			check := strings.Index(calls, " check -c ")
			if tc.invalid {
				if err == nil || replace >= 0 {
					t.Fatalf("invalid config replaced binary: %v %s", err, calls)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if check < 0 {
				t.Fatal("existing config not checked")
			}
			if tc.same {
				if strings.Contains(calls, node.SingBoxBinPath+".new") || replace >= 0 {
					t.Fatal("identical binary uploaded")
				}
			} else if replace < check {
				t.Fatal("replaced binary before config validation")
			}
			if !strings.Contains(r.unit, "ExecStart="+node.SingBoxBinPath+` run -c "/etc/mpx/sing-box/config.json"`) || !strings.Contains(calls, "systemctl enable 'mpx-sing-box'") {
				t.Fatalf("service was not independently installed: %s %s", r.unit, calls)
			}
			if strings.Contains(calls, "systemctl restart") {
				t.Fatal("installer started core without deploy")
			}
		})
	}
}

func installBinary(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sing-box")
	if err := os.WriteFile(p, []byte("candidate executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}
