package cli

import (
	"errors"
	"fmt"
	"testing"
)

func TestUIError(t *testing.T) {
	body := []byte(`{"error":"还没有管理员账号;在服务器上用 mpx admin create 建号(或由 mpx init 建)","ui_hint":"登录后到管理台页按提示建管理员账号"}`)
	ae := &APIError{StatusCode: 403, Message: errorMessage(403, body), UIHint: errorUIHint(body)}
	// 命令行照旧看原话。
	if ae.Error() != "还没有管理员账号;在服务器上用 mpx admin create 建号(或由 mpx init 建)" {
		t.Fatalf("Message = %q", ae.Error())
	}
	// 窗口路径换成界面说法,包一层也认得出来。
	got := uiError(fmt.Errorf("包一层: %w", ae))
	var out *APIError
	if !errors.As(got, &out) || out.Error() != "登录后到管理台页按提示建管理员账号" || out.StatusCode != 403 {
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
