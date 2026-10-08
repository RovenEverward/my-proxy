package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/store"
)

// newPopulatedStore 建一个带数据的库:一个节点、一个用户。库保持打开 ——
// 备份要能在控制面正常读写的时候做出来。
func newPopulatedStore(t *testing.T, dir string) (*store.Store, string) {
	t.Helper()
	dbPath := filepath.Join(dir, "mpx.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("打开测试库: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	n := &model.Node{
		Name: "hk", Address: "203.0.113.7", SSHPort: 2222, SSHUser: "ops",
		APIPort: 10085, ConfigPath: "/etc/xray/config.json", ServiceName: "xray",
	}
	if err := st.CreateNode(n); err != nil {
		t.Fatalf("建节点: %v", err)
	}
	u := &model.User{Username: "alice", Key: "alice-secret-key", SubToken: "tok-alice", Enabled: true}
	if err := st.CreateUser(u); err != nil {
		t.Fatalf("建用户: %v", err)
	}
	return st, dbPath
}

// tarGz 把一组文件条目打成 tar.gz,供坏输入用例拼归档。
func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(body))}); err != nil {
			t.Fatalf("写 %s 头: %v", name, err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("写 %s: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("收尾 tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("收尾 gzip: %v", err)
	}
	return buf.Bytes()
}

// otherDB 造一个合法的 SQLite 文件,但里面不是本系统的表。
func otherDB(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("打开: %v", err)
	}
	if _, err := db.Exec("CREATE TABLE other (x INTEGER)"); err != nil {
		t.Fatalf("建表: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("关闭: %v", err)
	}
}

// TestCreateValidateRoundTrip 覆盖最核心的一条:有数据的库 → 建归档 →
// 解归档 → 解出的 mpx.db 能开、数据在、manifest 数对。
func TestCreateValidateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, dbPath := newPopulatedStore(t, dir)

	// 控制面连节点用的私钥:存在时应当进归档。
	sshKey := filepath.Join(dir, ".ssh", "id_ed25519")
	if err := os.MkdirAll(filepath.Dir(sshKey), 0o700); err != nil {
		t.Fatalf("建 .ssh: %v", err)
	}
	if err := os.WriteFile(sshKey, []byte("FAKE-PRIVATE-KEY"), 0o600); err != nil {
		t.Fatalf("写私钥: %v", err)
	}

	var buf bytes.Buffer
	m, entries, err := Create(dbPath, sshKey, &buf)
	if err != nil {
		t.Fatalf("建归档: %v", err)
	}

	// manifest:行数对、表名在、含私钥标记。
	if got := m.RowCounts["nodes"]; got != 1 {
		t.Errorf("manifest nodes 行数 = %d, 想要 1", got)
	}
	if got := m.RowCounts["users"]; got != 1 {
		t.Errorf("manifest users 行数 = %d, 想要 1", got)
	}
	if !m.HasSSHKey {
		t.Error("manifest has_ssh_key = false, 想要 true")
	}
	for _, want := range []string{"nodes", "inbounds", "hosts", "users", "settings", "traffic_samples"} {
		found := false
		for _, got := range m.Tables {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("manifest 表清单缺 %s", want)
		}
	}

	// 归档条目:manifest.json、mpx.db、id_ed25519 三样。
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name] = true
	}
	for _, want := range []string{"manifest.json", "mpx.db", "id_ed25519"} {
		if !names[want] {
			t.Errorf("归档缺条目 %s", want)
		}
	}

	// manifest 是能被打印、被贴进工单的元数据,里面不能有凭证。
	if raw := string(readTarEntry(t, buf.Bytes(), "manifest.json")); strings.Contains(raw, "alice-secret-key") {
		t.Errorf("manifest.json 里出现了用户密钥: %s", raw)
	}

	// 解归档:拿到的快照能开、数据在。
	snap, m2, err := Validate(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("校验归档: %v", err)
	}
	t.Cleanup(func() { os.Remove(snap) })

	if m2.RowCounts["users"] != 1 || !m2.HasSSHKey {
		t.Errorf("解出的 manifest 不对: %+v", m2)
	}

	restored, err := store.Open(snap)
	if err != nil {
		t.Fatalf("打开解出的库: %v", err)
	}
	defer restored.Close()

	nodes, err := restored.ListNodes()
	if err != nil {
		t.Fatalf("读节点: %v", err)
	}
	if len(nodes) != 1 || nodes[0].Name != "hk" || nodes[0].Address != "203.0.113.7" {
		t.Errorf("恢复后的节点不对: %+v", nodes)
	}
	users, err := restored.ListUsers()
	if err != nil {
		t.Fatalf("读用户: %v", err)
	}
	if len(users) != 1 || users[0].Username != "alice" || users[0].Key != "alice-secret-key" ||
		users[0].SubToken != "tok-alice" {
		t.Errorf("恢复后的用户不对: %+v", users)
	}

	// 库还在被控制面开着,归档不该动它。
	if _, err := st.GetUser("alice"); err != nil {
		t.Errorf("备份后原库读不到用户: %v", err)
	}
}

// TestCreateWithoutSSHKey 私钥不存在时归档只有两样,manifest 记 false。
func TestCreateWithoutSSHKey(t *testing.T) {
	dir := t.TempDir()
	_, dbPath := newPopulatedStore(t, dir)

	var buf bytes.Buffer
	m, entries, err := Create(dbPath, filepath.Join(dir, "nope", "id_ed25519"), &buf)
	if err != nil {
		t.Fatalf("建归档: %v", err)
	}
	if m.HasSSHKey {
		t.Error("没有私钥时 has_ssh_key 应为 false")
	}
	for _, e := range entries {
		if e.Name == "id_ed25519" {
			t.Error("私钥不存在却进了归档")
		}
	}
}

// TestValidateRejectsBadInputs 四类坏输入都要报错,且不留下临时文件。
func TestValidateRejectsBadInputs(t *testing.T) {
	dir := t.TempDir()
	_, dbPath := newPopulatedStore(t, dir)
	// 一份好归档,用来取合法的 mpx.db 正文。
	var good bytes.Buffer
	if _, _, err := Create(dbPath, "", &good); err != nil {
		t.Fatalf("建归档: %v", err)
	}
	goodDB := readTarEntry(t, good.Bytes(), "mpx.db")

	notOurs := filepath.Join(dir, "not-ours.db")
	otherDB(t, notOurs)
	notOursBody, err := os.ReadFile(notOurs)
	if err != nil {
		t.Fatalf("读非本系统库: %v", err)
	}

	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"不是 gzip", []byte("this is not a gzip stream"), "不是 gzip 归档"},
		{"缺 mpx.db", tarGz(t, map[string]string{"manifest.json": `{"created_at":"2026-01-01T00:00:00Z"}`}), "归档里没有 mpx.db"},
		{"mpx.db 不是本项目的库", tarGz(t, map[string]string{
			"manifest.json": `{"tables":["nodes"]}`,
			"mpx.db":        string(notOursBody),
		}), "不是本系统的数据库"},
		{"manifest 坏", tarGz(t, map[string]string{
			"manifest.json": "{ not json",
			"mpx.db":        string(goodDB),
		}), "manifest.json 不是合法 JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snap, _, err := Validate(bytes.NewReader(tc.data))
			if err == nil {
				os.Remove(snap)
				t.Fatalf("想要报错,却成功了(snap=%s)", snap)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("错误 = %q, 想要包含 %q", err.Error(), tc.want)
			}
			if snap != "" {
				t.Errorf("出错时不该返回快照路径,得到 %q", snap)
			}
		})
	}
}

// readTarEntry 从一份 tar.gz 里取出某个条目的正文。
func readTarEntry(t *testing.T, data []byte, name string) []byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("解 gzip: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err != nil {
			t.Fatalf("归档里找不到 %s: %v", name, err)
		}
		if hdr.Name == name {
			var buf bytes.Buffer
			if _, err := buf.ReadFrom(tr); err != nil {
				t.Fatalf("读 %s: %v", name, err)
			}
			return buf.Bytes()
		}
	}
}

func TestDefaultFileName(t *testing.T) {
	// 用固定时间,免得测试依赖当下时区。
	got := DefaultFileName(time.Date(2026, 10, 8, 12, 26, 15, 0, time.UTC))
	if got != "mpx-backup-20261008-122615.tar.gz" {
		t.Errorf("默认文件名 = %q", got)
	}
}
