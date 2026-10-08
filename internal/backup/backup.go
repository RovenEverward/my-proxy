// Package backup 负责控制面数据库的备份与恢复归档。
//
// 归档是 gzip 压缩的 tar,包含三样东西:
//
//	manifest.json  — 备份元数据:创建时间、控制面主机名、各表行数、
//	                 是否含 SSH 私钥、库里的表名清单。不含任何密钥正文。
//	mpx.db         — 数据库的一致快照(由 VACUUM INTO 产生,单文件)。
//	id_ed25519     — 控制面连节点用的系统 ssh 私钥,存在才放。
//
// 管理 token 不放进来:它在 /etc/mpx/env 里,塞进归档等于把凭据复制进一个
// 会被到处拷的文件(理由见 docs/decisions.md 第 16 条)。
//
// 本包只用标准库(archive/tar、compress/gzip)加 modernc.org/sqlite
// (仓库已有的驱动,store 包也在用),不新增任何依赖。
package backup

import (
	"archive/tar"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // 驱动名 "sqlite",与 store 包一致
)

// Manifest 是归档里 manifest.json 的内容,描述一次备份的元数据。
//
// 刻意只放"能帮人判断这份备份是否可用"的信息,不放任何密钥正文:
// 这份 JSON 会被打印、会被贴进工单,不能让它成为又一个泄密的载体。
type Manifest struct {
	CreatedAt time.Time        `json:"created_at"`
	Hostname  string           `json:"hostname"`
	Tables    []string         `json:"tables"`
	RowCounts map[string]int64 `json:"row_counts"`
	HasSSHKey bool             `json:"has_ssh_key"`
}

// Entry 是归档中的一个文件条目,供调用方(日志、测试)核对。
type Entry struct {
	Name string
	Size int64
}

// DefaultFileName 返回默认备份文件名:mpx-backup-<yyyymmdd-hhmmss>.tar.gz。
//
// 用本地时间:文件名是给人看的,和用户当下在哪儿对得上才有意义。归档内
// manifest 的 created_at 则是机器的记录,用 UTC。
func DefaultFileName(now time.Time) string {
	return fmt.Sprintf("mpx-backup-%s.tar.gz", now.Format("20060102-150405"))
}

// Create 把数据库的一致快照写成一个归档。
//
//   - dbPath 是控制面正在使用的 SQLite 文件路径。
//   - sshKeyPath 是系统 ssh 私钥路径(通常是 <db 目录>/.ssh/id_ed25519);
//     为空或文件不存在时归档不含私钥,manifest 里 has_ssh_key 记 false。
//   - w 接收 gzip 后的 tar 字节流。
//
// 快照用 VACUUM INTO 得到:它把当前库(含 WAL 里未落盘的数据)写成一个自洽的
// 单文件,不锁写、不用停服务,也没有 -wal/-shm 跟随。目标路径落在库同目录的
// 随机临时文件上,避免 VACUUM INTO 遇到已存在目标时的报错,写进归档后即删。
func Create(dbPath, sshKeyPath string, w io.Writer) (*Manifest, []Entry, error) {
	if dbPath == "" {
		return nil, nil, errors.New("备份: 数据库路径为空")
	}

	db, err := sql.Open("sqlite", dsn(dbPath))
	if err != nil {
		return nil, nil, fmt.Errorf("打开数据库 %s: %w", dbPath, err)
	}
	defer db.Close()

	snapPath, err := snapshotDB(db, dbPath)
	if err != nil {
		return nil, nil, err
	}
	defer os.Remove(snapPath) // 快照是临时文件,写进归档后即删

	m, err := buildManifest(db, sshKeyPath)
	if err != nil {
		return nil, nil, err
	}

	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)

	// 出错路径靠 defer 收尾;成功路径在函数尾显式 Close 并检查错误。
	closeOK := false
	defer func() {
		if !closeOK {
			tw.Close()
			gz.Close()
		}
	}()

	var entries []Entry

	manifestBytes, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("序列化 manifest: %w", err)
	}
	if err := writeTarBytes(tw, "manifest.json", manifestBytes, 0o644); err != nil {
		return nil, nil, err
	}
	entries = append(entries, Entry{Name: "manifest.json", Size: int64(len(manifestBytes))})

	snapSize, err := writeTarFile(tw, "mpx.db", snapPath, 0o600)
	if err != nil {
		return nil, nil, err
	}
	entries = append(entries, Entry{Name: "mpx.db", Size: snapSize})

	if sshKeyPath != "" {
		if _, err := os.Stat(sshKeyPath); err == nil {
			keySize, err := writeTarFile(tw, "id_ed25519", sshKeyPath, 0o600)
			if err != nil {
				return nil, nil, err
			}
			entries = append(entries, Entry{Name: "id_ed25519", Size: keySize})
		} else if !os.IsNotExist(err) {
			return nil, nil, fmt.Errorf("检查 ssh 私钥 %s: %w", sshKeyPath, err)
		}
	}

	if err := tw.Close(); err != nil {
		return nil, nil, fmt.Errorf("收尾 tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, nil, fmt.Errorf("收尾 gzip: %w", err)
	}
	closeOK = true
	return m, entries, nil
}

// dsn 拼出与 store 包一致的 DSN:路径转义 + WAL 等 pragma。备份只读库,
// 但 busy_timeout 必须带上:控制面正在写库时,没它 VACUUM INTO 会立刻撞
// database is locked。
const pragmas = "_pragma=journal_mode(WAL)" +
	"&_pragma=busy_timeout(5000)" +
	"&_pragma=foreign_keys(ON)" +
	"&_pragma=synchronous(NORMAL)"

func dsn(path string) string {
	u := url.URL{Path: path}
	return "file:" + u.EscapedPath() + "?" + pragmas
}

// snapshotDB 执行 VACUUM INTO,把当前库的一致快照写到临时文件并返回其路径。
//
// 实测过 modernc.org/sqlite 对 VACUUM INTO 的支持:占位符形式(参数绑定)与
// 字面量形式都能用;WAL 模式下未 checkpoint 的数据也进快照;目标已存在时报错
// (所以这里先确保目标不存在)。
func snapshotDB(db *sql.DB, dbPath string) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(dbPath), ".mpx-snapshot-*.db")
	if err != nil {
		return "", fmt.Errorf("创建快照临时文件: %w", err)
	}
	snapPath := f.Name()
	f.Close()
	os.Remove(snapPath) // VACUUM INTO 要求目标不存在

	if _, err := db.Exec("VACUUM INTO ?", snapPath); err != nil {
		os.Remove(snapPath)
		return "", fmt.Errorf("VACUUM INTO 快照: %w", err)
	}
	return snapPath, nil
}

// buildManifest 读库结构统计出备份元数据。表名来自 sqlite_master,是库自己
// 的内容不是外部输入,直接拼进 SQL 安全;行数逐表 COUNT。
func buildManifest(db *sql.DB, sshKeyPath string) (*Manifest, error) {
	hostname, _ := os.Hostname()

	rows, err := db.Query(
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("列出表: %w", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return nil, fmt.Errorf("读取表名: %w", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("遍历表名: %w", err)
	}
	rows.Close()

	counts := make(map[string]int64, len(tables))
	for _, t := range tables {
		var n int64
		if err := db.QueryRow("SELECT COUNT(*) FROM " + t).Scan(&n); err != nil {
			return nil, fmt.Errorf("统计 %s 行数: %w", t, err)
		}
		counts[t] = n
	}

	m := &Manifest{
		CreatedAt: time.Now().UTC(),
		Hostname:  hostname,
		Tables:    tables,
		RowCounts: counts,
	}
	if sshKeyPath != "" {
		if _, err := os.Stat(sshKeyPath); err == nil {
			m.HasSSHKey = true
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("检查 ssh 私钥 %s: %w", sshKeyPath, err)
		}
	}
	return m, nil
}

func writeTarBytes(tw *tar.Writer, name string, data []byte, mode int64) error {
	hdr := &tar.Header{Name: name, Mode: mode, Size: int64(len(data))}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("写 %s 头: %w", name, err)
	}
	if _, err := tw.Write(data); err != nil {
		return fmt.Errorf("写 %s: %w", name, err)
	}
	return nil
}

// writeTarFile 把本地文件作为条目写进 tar,返回写入的字节数。
func writeTarFile(tw *tar.Writer, name, path string, mode int64) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("打开 %s: %w", path, err)
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return 0, fmt.Errorf("读取 %s 大小: %w", path, err)
	}
	hdr := &tar.Header{Name: name, Mode: mode, Size: st.Size()}
	if err := tw.WriteHeader(hdr); err != nil {
		return 0, fmt.Errorf("写 %s 头: %w", name, err)
	}
	n, err := io.Copy(tw, f)
	if err != nil {
		return 0, fmt.Errorf("写 %s: %w", name, err)
	}
	return n, nil
}
