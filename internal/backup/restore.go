package backup

import (
	"archive/tar"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/store"
)

// Validate 校验一份归档是不是本系统能认的备份,并把里头的数据库快照落盘、
// 迁移到当前 schema。
//
// 返回写好的 mpx.db 快照文件路径(临时目录,已用 store.Open 迁移过)与
// manifest。调用方负责把快照放到最终位置。
//
// 校验顺序:gzip/tar 能解 → 有 mpx.db → manifest 能解析 → 先裸开一次确认
// 它本来就是本系统的库(六个核心表都在)→ 再用 store.Open 打开(旧备份顺带
// 迁移到当前 schema)→ 确认 nodes/users/settings 能读。前两步任一失败都不
// 动调用方的任何文件。
//
// 先裸查再 Open 是有意的:store.Open 对任意 SQLite 文件都会把缺的表建出来,
// 单靠它会把一个不相干的库"洗"成本系统的样子放过去。先查表存在,才能把
// 不是本系统的文件挡在门外。
func Validate(r io.Reader) (dbPath string, m *Manifest, err error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return "", nil, fmt.Errorf("不是 gzip 归档: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)

	var (
		manifestBytes []byte
		snapName      string // mpx.db 解出的临时文件路径
	)

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", nil, fmt.Errorf("读取 tar: %w", err)
		}
		switch hdr.Name {
		case "manifest.json":
			b, err := io.ReadAll(tr)
			if err != nil {
				return "", nil, fmt.Errorf("读取 manifest.json: %w", err)
			}
			manifestBytes = b
		case "mpx.db":
			f, err := os.CreateTemp("", ".mpx-restore-*.db")
			if err != nil {
				return "", nil, fmt.Errorf("创建快照临时文件: %w", err)
			}
			snapName = f.Name()
			_, err = io.Copy(f, tr)
			cerr := f.Close()
			if err != nil {
				os.Remove(snapName)
				return "", nil, fmt.Errorf("解出 mpx.db: %w", err)
			}
			if cerr != nil {
				os.Remove(snapName)
				return "", nil, fmt.Errorf("关闭快照临时文件: %w", cerr)
			}
		}
	}

	if snapName == "" {
		return "", nil, errors.New("归档里没有 mpx.db")
	}
	// 下面任何一步失败都要清掉临时文件(连同 WAL 模式的 -wal/-shm)。
	ok := false
	defer func() {
		if !ok {
			os.Remove(snapName)
			os.Remove(snapName + "-wal")
			os.Remove(snapName + "-shm")
		}
	}()

	var manifest Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return "", nil, fmt.Errorf("manifest.json 不是合法 JSON: %w", err)
	}

	if err := validateDB(snapName); err != nil {
		return "", nil, err
	}

	ok = true
	return snapName, &manifest, nil
}

// validateDB 先裸查表、再用 store.Open 迁移,确认快照是本系统的库且能读。
//
// 关闭时 SQLite 会把 WAL 里的迁移结果 checkpoint 进主文件;之后那两个 -wal/-shm
// 就是残留,一并清掉,免得调用方拿到的"一份 mpx.db"旁边还跟着两个文件。
func validateDB(path string) error {
	if err := checkOwnTables(path); err != nil {
		return err
	}

	st, err := store.Open(path)
	if err != nil {
		return fmt.Errorf("打开快照库: %w", err)
	}
	fail := func(err error) error {
		st.Close()
		return err
	}

	// 迁移后核心表必然在(store.Open 的建表逻辑),这里走一遍 store 的公共读取
	// 路径确认它们能读:数据有没有另说,SQL 必须跑得通,否则这份备份恢复进去
	// 也是坏的。
	if _, err := st.ListNodes(); err != nil {
		return fail(fmt.Errorf("快照库的 nodes 表读不了: %w", err))
	}
	if _, err := st.ListUsers(); err != nil {
		return fail(fmt.Errorf("快照库的 users 表读不了: %w", err))
	}
	if _, err := st.GetSettings(); err != nil {
		return fail(fmt.Errorf("快照库的 settings 表读不了: %w", err))
	}
	if err := st.Close(); err != nil {
		return fmt.Errorf("关闭快照库: %w", err)
	}

	os.Remove(path + "-wal")
	os.Remove(path + "-shm")
	return nil
}

// checkOwnTables 裸开快照库,确认它原本就是本系统的库(六个核心表都在),
// 不依赖 store.Open 帮我们建表。表名来自 sqlite_master,是库自己读出来的,
// 不是外部输入,直接拼进 SQL 安全。
func checkOwnTables(path string) error {
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return fmt.Errorf("打开快照库: %w", err)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		return fmt.Errorf("读取快照库的表: %w", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return fmt.Errorf("读取快照库的表名: %w", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("遍历快照库的表: %w", err)
	}
	rows.Close()

	required := []string{"nodes", "inbounds", "hosts", "users", "settings", "traffic_samples"}
	have := map[string]bool{}
	for _, t := range tables {
		have[t] = true
	}
	var missing []string
	for _, t := range required {
		if !have[t] {
			missing = append(missing, t)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("不是本系统的数据库(缺表: %s)", strings.Join(missing, ", "))
	}
	return nil
}

// SSHKeyPath 返回控制面的系统 ssh 私钥路径:<db 目录>/.ssh/id_ed25519。
// 生产部署里 install.sh 把密钥生成在这个位置(deploy/install.sh 第 87 行)。
func SSHKeyPath(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), ".ssh", "id_ed25519")
}
