package server

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/backup"
	"github.com/RovenEverward/my-proxy/internal/store"
)

// backupArchive 用测试库现场做一份归档,替测试省掉手工拼 tar 的力气。
func backupArchive(t *testing.T, e *env) []byte {
	t.Helper()
	var buf bytes.Buffer
	if _, _, err := backup.Create(e.dbPath, backup.SSHKeyPath(e.dbPath), &buf); err != nil {
		t.Fatalf("建归档: %v", err)
	}
	return buf.Bytes()
}

// TestBackupEndpointRequiresAuth:备份/恢复是管理接口,没带 token 应当 401。
func TestBackupEndpointRequiresAuth(t *testing.T) {
	e := newEnv(t)
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/backup", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /api/backup 无 token: got %d, 想要 401", resp.StatusCode)
	}
}

// TestRestoreFlowOfflineSwap 走完用户视角的完整流程:备份 → 删库 → 手动把库
// 放进 .restore → 启动控制面(Serve)→ 数据回来了、旧库留在 pre-restore。
func TestRestoreFlowOfflineSwap(t *testing.T) {
	// 第一步:起一个控制面,建数据,出一份备份。
	e := newEnv(t)
	n := e.createNode("hk", "node.example.com")
	in := e.createInbound(n.ID, "vless-ws", 8443)
	e.createHost(in.ID, "香港 ws tls", "node.example.com")
	e.createUser("alice")
	archive := backupArchive(t, e)
	e.srv.Close()
	if err := e.st.Close(); err != nil {
		t.Fatalf("关掉测试库: %v", err)
	}

	dbPath := e.dbPath

	// 第二步:模拟"库没了"——删掉主库文件,把备份里那份 mpx.db 放到 .restore。
	snap, _, err := backup.Validate(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("解归档: %v", err)
	}
	body, err := os.ReadFile(snap)
	if err != nil {
		t.Fatalf("读快照: %v", err)
	}
	os.Remove(snap)
	os.Remove(dbPath)
	if err := os.WriteFile(dbPath+".restore", body, 0o600); err != nil {
		t.Fatalf("放暂存文件: %v", err)
	}

	// 第三步:直接调换入函数(Serve 里也是先换入再开库)。主库已经删了,没有旧库
	// 可留,pre-restore 为空是正常的;旧库保留由 TestApplyStagedRestoreSwapsIn 覆盖。
	staged, _, err := applyStagedRestore(dbPath)
	if err != nil {
		t.Fatalf("applyStagedRestore: %v", err)
	}
	if staged == "" {
		t.Fatal("应当换入暂存文件, staged 为空")
	}

	// 第四步:新控制面打开换入后的库,数据都在、能出订阅。
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("打开换入后的库: %v", err)
	}
	defer st.Close()
	nodes, err := st.ListNodes()
	if err != nil || len(nodes) != 1 || nodes[0].Name != "hk" {
		t.Fatalf("节点没回来: %v %+v", err, nodes)
	}
	u, err := st.GetUser("alice")
	if err != nil {
		t.Fatalf("用户没回来: %v", err)
	}
	if u.SubToken == "" {
		t.Fatal("订阅 token 没回来")
	}
	srv := New(st, testToken, logger, dbPath)
	hs := httptest.NewServer(srv.Handler())
	defer hs.Close()

	// 路径里点名 links:这里验的是"数据回来了、能出订阅",不是 UA 分流,别让
	// 内置规则把 v2rayng 认成 base64 之后连正文都找不到。
	req, _ := http.NewRequest("GET", hs.URL+"/sub/"+u.SubToken+"/links", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("订阅请求: %v", err)
	}
	subBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(subBody), "vless://") {
		t.Fatalf("恢复后订阅失败: %d %s", resp.StatusCode, subBody)
	}
}
func TestBackupEndpointReturnsUsableArchive(t *testing.T) {
	e := newEnv(t)
	e.createNode("hk", "203.0.113.7")
	e.createUser("alice")

	code, data := e.api("GET", "/api/backup", nil)
	if code != 200 {
		t.Fatalf("GET /api/backup: got %d %s", code, data)
	}

	snap, m, err := backup.Validate(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("解归档: %v", err)
	}
	t.Cleanup(func() { os.Remove(snap) })

	if m.RowCounts["nodes"] != 1 || m.RowCounts["users"] != 1 {
		t.Errorf("manifest 行数不对: %+v", m.RowCounts)
	}

	st, err := store.Open(snap)
	if err != nil {
		t.Fatalf("打开解出的库: %v", err)
	}
	defer st.Close()
	nodes, err := st.ListNodes()
	if err != nil || len(nodes) != 1 || nodes[0].Name != "hk" {
		t.Errorf("解出的节点不对: %v %+v", err, nodes)
	}
	if _, err := st.GetUser("alice"); err != nil {
		t.Errorf("解出的库里没有 alice: %v", err)
	}
}

// TestRestoreEndpointStagesValidArchive:合法归档 → 200,<db>.restore 存在且是那份库。
func TestRestoreEndpointStagesValidArchive(t *testing.T) {
	e := newEnv(t)
	e.createNode("hk", "203.0.113.7")
	e.createUser("alice")

	archive := backupArchive(t, e)
	staged := e.dbPath + ".restore"

	code, data := e.requestRaw("POST", "/api/restore", archive)
	if code != 200 {
		t.Fatalf("POST /api/restore: got %d %s", code, data)
	}
	if !strings.Contains(string(data), "重启") {
		t.Errorf("响应没告诉用户重启后生效: %s", data)
	}
	if _, err := os.Stat(staged); err != nil {
		t.Fatalf("暂存文件不存在: %v", err)
	}

	// 暂存的确实是那份库:打开能看到里面的数据。
	st, err := store.Open(staged)
	if err != nil {
		t.Fatalf("打开暂存库: %v", err)
	}
	defer st.Close()
	if _, err := st.GetUser("alice"); err != nil {
		t.Errorf("暂存库里没有 alice: %v", err)
	}
}

// TestRestoreEndpointRejectsBadArchive:非法归档 400,且不留下暂存文件。
func TestRestoreEndpointRejectsBadArchive(t *testing.T) {
	e := newEnv(t)

	code, data := e.requestRaw("POST", "/api/restore", []byte("这不是 tar.gz"))
	if code != 400 {
		t.Fatalf("非法归档: got %d %s", code, data)
	}
	if _, err := os.Stat(e.dbPath + ".restore"); !os.IsNotExist(err) {
		t.Errorf("非法归档不该留下暂存文件, stat err = %v", err)
	}
}

// TestRestoreEndpointRefusesExistingStaged:已有一份暂存时拒绝,?force=1 才覆盖。
func TestRestoreEndpointRefusesExistingStaged(t *testing.T) {
	e := newEnv(t)
	e.createUser("alice")
	staged := e.dbPath + ".restore"
	if err := os.WriteFile(staged, []byte("上一份暂存"), 0o600); err != nil {
		t.Fatalf("预置暂存文件: %v", err)
	}

	archive := backupArchive(t, e)
	code, data := e.requestRaw("POST", "/api/restore", archive)
	if code != 409 {
		t.Fatalf("已有暂存时应拒绝: got %d %s", code, data)
	}
	if got, _ := os.ReadFile(staged); string(got) != "上一份暂存" {
		t.Errorf("被拒绝时不该动暂存文件,现在内容 = %q", got)
	}

	code, data = e.requestRaw("POST", "/api/restore?force=1", archive)
	if code != 200 {
		t.Fatalf("带 force 应覆盖: got %d %s", code, data)
	}
	st, err := store.Open(staged)
	if err != nil {
		t.Fatalf("打开覆盖后的暂存库: %v", err)
	}
	defer st.Close()
	if _, err := st.GetUser("alice"); err != nil {
		t.Errorf("覆盖后的暂存库里没有 alice: %v", err)
	}
}
