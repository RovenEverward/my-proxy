package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// openAdminStore 建一个内存临时库;本文件的用例只跟 admin 两表打交道。
func openAdminStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "mpx.db"))
	if err != nil {
		t.Fatalf("打开测试库: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestAdminAccountLifecycle(t *testing.T) {
	st := openAdminStore(t)
	now := time.Now()

	// 没建过:ErrNotFound,不是空串 —— 「还没初始化」要能被调用方分出来。
	if _, _, err := st.AdminAccount(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("空库应是 ErrNotFound,得到 %v", err)
	}

	if err := st.CreateAdminAccount("ops", "$argon2id$v=19$fake", now); err != nil {
		t.Fatalf("建号: %v", err)
	}
	u, h, err := st.AdminAccount()
	if err != nil || u != "ops" || h != "$argon2id$v=19$fake" {
		t.Fatalf("读回不对: %q %q %v", u, h, err)
	}

	// 同 id 再建行撞 CHECK(id=1):第二道闸兜住并发撞号。
	if err := st.CreateAdminAccount("ops2", "h2", now); err == nil {
		t.Fatal("已有账号再建应该报错")
	}

	if err := st.SetAdminUsername("admin"); err != nil {
		t.Fatalf("改名: %v", err)
	}
	if u, _, _ := st.AdminAccount(); u != "admin" {
		t.Fatalf("改名后读回 %q", u)
	}
	if err := st.SetAdminPasswordHash("newhash"); err != nil {
		t.Fatalf("改口令: %v", err)
	}
	if _, h, _ := st.AdminAccount(); h != "newhash" {
		t.Fatalf("改口令后读回 %q", h)
	}
}

func TestAdminSessions(t *testing.T) {
	st := openAdminStore(t)
	now := time.Now()
	later := now.Add(24 * time.Hour)
	expired := now.Add(-time.Hour)

	// 查不到与已过期都算 ErrNotFound。
	if _, err := st.AdminSessionByTokenHash("nope", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("没有会话应 ErrNotFound,得到 %v", err)
	}
	if err := st.CreateAdminSession("h-old", "web", now.Add(-2*time.Hour), expired); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AdminSessionByTokenHash("h-old", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("过期会话应 ErrNotFound,得到 %v", err)
	}

	if err := st.CreateAdminSession("h-web", "web", now, later); err != nil {
		t.Fatalf("建会话: %v", err)
	}
	if err := st.CreateAdminSession("h-cli", "cli", now, later); err != nil {
		t.Fatalf("建 cli 会话: %v", err)
	}
	s, err := st.AdminSessionByTokenHash("h-web", now)
	if err != nil || s.Kind != "web" || s.ExpiresAt.Unix() != later.Unix() {
		t.Fatalf("查回不对: %+v %v", s, err)
	}

	if err := st.DeleteAdminSession("h-web"); err != nil {
		t.Fatalf("吊销: %v", err)
	}
	if _, err := st.AdminSessionByTokenHash("h-web", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("吊销后应 ErrNotFound,得到 %v", err)
	}
	if err := st.DeleteAdminSession("h-web"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("重复吊销应 ErrNotFound,得到 %v", err)
	}

	// 整表清空(改密码的吊销动作)。
	if n, err := st.DeleteAllAdminSessions(); err != nil || n != 2 { // h-cli + 已过期那条
		t.Fatalf("清空 %d %v", n, err)
	}
	if _, err := st.AdminSessionByTokenHash("h-cli", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("清空后应 ErrNotFound,得到 %v", err)
	}
}
