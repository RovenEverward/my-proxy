package control

import (
	"log/slog"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// ControlUpdate 与 /api/status 报的必须是同一件事:两处对不上时,使用者没法知道
// 该信哪个。所以它只能是 updateInfo 的一份翻译,不能各算各的。
func TestControlUpdateMirrorsStatusUpdate(t *testing.T) {
	task, _, _ := taskWithClock(t, &scriptedRunner{}, slog.New(slog.DiscardHandler))

	got := task.ControlUpdate()
	want := model.UpdatePartFrom(task.updateInfo())
	if got != want {
		t.Fatalf("ControlUpdate 与 updateInfo 不一致:\n got %+v\nwant %+v", got, want)
	}
}

// 一次都没查过时只有本机版本:不能声称有新版,也不能凭空给一个最新版本。
func TestControlUpdateWithoutAnyCheckClaimsNothing(t *testing.T) {
	task, _, _ := taskWithClock(t, &scriptedRunner{}, slog.New(slog.DiscardHandler))

	got := task.ControlUpdate()
	if got.Available || got.Latest != "" || got.CheckedAt != nil || got.ReleaseURL != "" {
		t.Errorf("还没查过时不该有任何更新结论:%+v", got)
	}
	// go test 编出来的二进制没有版本号;"未知"这一支在 M1 的验收里要真的显示成
	// 「未知」,而它不该因此被判成"有新版"。
	if got.Current != "" {
		t.Errorf("测试二进制不该带版本号,实际 %q", got.Current)
	}
	if got.Available {
		t.Error("版本未知时永远不能说有新版")
	}
}
