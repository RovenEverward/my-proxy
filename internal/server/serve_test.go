package server

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/backup"
	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/store"
)

// makeStoreAt 在指定路径建一个只有一个节点的库,返回时已关闭 —— 换入发生在
// 控制面打开库之前,所以这里不能留着打开的连接。
func makeStoreAt(t *testing.T, dbPath, nodeName string) {
	t.Helper()
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("打开 %s: %v", dbPath, err)
	}
	if err := st.CreateNode(&model.Node{Name: nodeName, Address: "203.0.113.7"}); err != nil {
		t.Fatalf("建节点: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("关闭 %s: %v", dbPath, err)
	}
}

func nodeNames(t *testing.T, dbPath string) []string {
	t.Helper()
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("打开 %s: %v", dbPath, err)
	}
	defer st.Close()
	nodes, err := st.ListNodes()
	if err != nil {
		t.Fatalf("读 %s 的节点: %v", dbPath, err)
	}
	names := make([]string, 0, len(nodes))
	for _, n := range nodes {
		names = append(names, n.Name)
	}
	return names
}

// TestApplyStagedRestoreNoStaged:没有 .restore 时什么都不做。
func TestApplyStagedRestoreNoStaged(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "mpx.db")
	makeStoreAt(t, dbPath, "old")

	staged, preRestore, err := applyStagedRestore(dbPath)
	if err != nil {
		t.Fatalf("applyStagedRestore: %v", err)
	}
	if staged != "" || preRestore != "" {
		t.Errorf("没有暂存文件时不该有动作: staged=%q preRestore=%q", staged, preRestore)
	}
	if got := nodeNames(t, dbPath); len(got) != 1 || got[0] != "old" {
		t.Errorf("库被动了: %v", got)
	}
}

// TestApplyStagedRestoreSwapsIn:预置一份装着"另一份库"的 .restore,换入后主库是
// 快照的数据、旧库留在 <db>.pre-restore-*、.restore 没了。
func TestApplyStagedRestoreSwapsIn(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mpx.db")
	makeStoreAt(t, dbPath, "old")

	// 另一份库:一个叫 new 的节点。从它做一份归档再解出库文件,模拟用户把一份
	// 备份放到 .restore。
	otherDB := filepath.Join(t.TempDir(), "other.db")
	makeStoreAt(t, otherDB, "new")
	var buf bytes.Buffer
	if _, _, err := backup.Create(otherDB, "", &buf); err != nil {
		t.Fatalf("建归档: %v", err)
	}
	snap, _, err := backup.Validate(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("解归档: %v", err)
	}
	staged := dbPath + ".restore"
	body, err := os.ReadFile(snap)
	if err != nil {
		t.Fatalf("读快照: %v", err)
	}
	os.Remove(snap)
	if err := os.WriteFile(staged, body, 0o600); err != nil {
		t.Fatalf("放暂存文件: %v", err)
	}

	gotStaged, preRestore, err := applyStagedRestore(dbPath)
	if err != nil {
		t.Fatalf("applyStagedRestore: %v", err)
	}
	if gotStaged != staged {
		t.Errorf("staged = %q, 想要 %q", gotStaged, staged)
	}
	if !strings.HasPrefix(preRestore, dbPath+".pre-restore-") {
		t.Errorf("preRestore = %q, 想要 %s.pre-restore-*", preRestore, dbPath)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Errorf(".restore 应该没了, stat err = %v", err)
	}
	if got := nodeNames(t, dbPath); len(got) != 1 || got[0] != "new" {
		t.Errorf("主库不是快照的数据: %v", got)
	}
	if got := nodeNames(t, preRestore); len(got) != 1 || got[0] != "old" {
		t.Errorf("旧库内容不对: %v", got)
	}

	// 再调一次:没有暂存文件了,应当是空操作。
	staged2, preRestore2, err := applyStagedRestore(dbPath)
	if err != nil || staged2 != "" || preRestore2 != "" {
		t.Errorf("第二次调用应当空操作: staged=%q preRestore=%q err=%v", staged2, preRestore2, err)
	}
}

// TestApplyStagedRestoreWithoutLiveDB:主库不在(比如第一次恢复之前就被删了)
// 也能换入,暂存文件直接成为主库。
func TestApplyStagedRestoreWithoutLiveDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mpx.db")

	fresh := filepath.Join(t.TempDir(), "fresh.db")
	makeStoreAt(t, fresh, "fresh")
	staged := dbPath + ".restore"
	body, err := os.ReadFile(fresh)
	if err != nil {
		t.Fatalf("读库: %v", err)
	}
	if err := os.WriteFile(staged, body, 0o600); err != nil {
		t.Fatalf("放暂存文件: %v", err)
	}

	gotStaged, preRestore, err := applyStagedRestore(dbPath)
	if err != nil {
		t.Fatalf("applyStagedRestore: %v", err)
	}
	if gotStaged == "" {
		t.Error("staged 不该为空")
	}
	if preRestore != "" {
		t.Errorf("没有旧库时不该有 preRestore,得到 %q", preRestore)
	}
	if got := nodeNames(t, dbPath); len(got) != 1 || got[0] != "fresh" {
		t.Errorf("主库不是快照的数据: %v", got)
	}
}
