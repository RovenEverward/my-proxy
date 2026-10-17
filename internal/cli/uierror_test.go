package cli

import (
	"errors"
	"fmt"
	"testing"
)

func TestUIError(t *testing.T) {
	body := []byte(`{"error":"这个控制面还没设设备密码;在已接入的设备上跑 mpx device password","ui_hint":"在已接入的设备上打开 设置 → 设备 设一个"}`)
	ae := &APIError{StatusCode: 403, Message: errorMessage(403, body), UIHint: errorUIHint(body)}
	// 命令行照旧看原话。
	if ae.Error() != "这个控制面还没设设备密码;在已接入的设备上跑 mpx device password" {
		t.Fatalf("Message = %q", ae.Error())
	}
	// 窗口路径换成界面说法,包一层也认得出来。
	got := uiError(fmt.Errorf("包一层: %w", ae))
	var out *APIError
	if !errors.As(got, &out) || out.Error() != "在已接入的设备上打开 设置 → 设备 设一个" || out.StatusCode != 403 {
		t.Fatalf("uiError = %v", got)
	}
	// 没有 ui_hint 的错误原样返回。
	plain := errors.New("别的错")
	if uiError(plain) != plain {
		t.Fatal("没有 ui_hint 时应原样返回")
	}
	if errorUIHint([]byte(`{"error":"x"}`)) != "" || errorUIHint([]byte("not json")) != "" {
		t.Fatal("没有 ui_hint 时应为空")
	}
}
