package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 页面更新必须在后台任务和远程请求开始前拒绝，不能先升级线上再在本机一步失败。
func TestDevelopmentRejectsUpdatesBeforeWork(t *testing.T) {
	withLocalVersion(t, "dev-123456789abc-clean")
	oldSource := updateSource
	t.Cleanup(func() { updateSource = oldSource })
	updateSource = nil
	s := &Server{}
	_, _, changed, _, code, err := s.applyCLIUpdate(context.Background(), "v0.9.13")
	if code != http.StatusConflict || changed || err == nil || !strings.Contains(err.Error(), "mpx-dev-update") {
		t.Fatalf("更新未被拒绝：code=%d changed=%v err=%v", code, changed, err)
	}
	w := httptest.NewRecorder()
	s.updateAllStart(w, httptest.NewRequest(http.MethodPost, "/local/update/all", nil))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "mpx-dev-update") {
		t.Fatalf("一键更新未被拒绝：%d %s", w.Code, w.Body)
	}
}
