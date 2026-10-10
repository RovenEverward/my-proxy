package diagnostics

import (
	"testing"
	"time"
)

func TestFusionConfigurationIndependentBudget(t *testing.T) {
	cfg, err := (Config{Engine: "fusion", Speed: true}).Normalized()
	if err != nil || cfg.Position != "node" || cfg.FusionTimeoutSeconds != 900 || cfg.MaxBytes != 0 || cfg.TotalTimeoutSeconds != 0 {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.CheckupTimeout() != 18*time.Minute || (Config{Engine: "fusion", FusionTimeoutSeconds: 1800}).CheckupTimeout() != 33*time.Minute || (Config{Engine: "light"}).CheckupTimeout() != 6*time.Minute || (Config{Position: "control"}).CheckupTimeout() != 6*time.Minute {
		t.Fatal("full budget drifted")
	}
	for _, bad := range []Config{
		{Engine: "other"}, {Engine: "fusion", Position: "control"}, {Engine: "fusion", Position: "local"}, {Engine: "fusion", MaxBytes: 1024}, {Engine: "fusion", Samples: 3}, {Engine: "fusion", Targets: DefaultTargets()}, {Engine: "fusion", TotalTimeoutSeconds: 120}, {Engine: "fusion", SocksURL: "socks5h://127.0.0.1:1080"}, {Engine: "fusion", FusionTimeoutSeconds: 59}, {Engine: "fusion", FusionTimeoutSeconds: 1801}, {Engine: "light", Hardware: true}, {Engine: "light", FusionTimeoutSeconds: 900},
	} {
		if err := bad.Validate(); err == nil {
			t.Fatalf("invalid option accepted: %+v", bad)
		}
	}
}
