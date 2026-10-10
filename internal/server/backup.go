package server

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/RovenEverward/my-proxy/internal/backup"
)

// handleBackup 流式返回一份 tar.gz 备份归档。
//
// 正文就是 gzip 的 tar 字节流,不是 JSON 包装:CLI 直接落盘,curl 也能
// 重定向到文件。归档里的东西见 backup 包:manifest.json + mpx.db + 可选的
// id_ed25519,不含管理 token(理由见 docs/internal/decisions.md 第 16 条)。
//
// 备份本身不锁库:快照由 VACUUM INTO 产生,控制面正常读写时也能出。
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	path := s.dbPath
	if path == "" {
		writeError(w, http.StatusInternalServerError, "控制面没有数据库路径")
		return
	}

	filename := backup.DefaultFileName(time.Now())
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)

	if _, _, err := backup.Create(path, backup.SSHKeyPath(path), w); err != nil {
		// 头部已经写出去,不能再改状态码;记日志并断开连接,客户端会看到
		// 一个截断的 gzip 流。
		s.logger.Error("生成备份失败", "error", err)
		return
	}
}

// handleRestore 校验并暂存一份备份归档,真正的换入留到下次启动。
//
// 正文就是归档本身(非 JSON 包装)。只做两件事:
//  1. 校验:能解 gzip/tar、有 mpx.db、manifest 能解析、库能开且是本系统的;
//  2. 暂存:把校验过的 mpx.db 写到 <db 路径>.restore(0600),返回 200 并提示
//     "已就绪,重启 mpx 后生效"。
//
// 已存在 .restore 时拒绝,?force=1 才覆盖。校验不通过时**不**动已有的
// .restore:宁可保留旧的就绪状态,也不拿坏归档顶掉它。
func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	path := s.dbPath
	if path == "" {
		writeError(w, http.StatusInternalServerError, "控制面没有数据库路径")
		return
	}
	stagedPath := path + ".restore"

	// 有暂存文件时先看要不要覆盖。覆盖发生在校验之后,所以校验失败时已有的
	// .restore 保持原样。
	if _, err := os.Stat(stagedPath); err == nil {
		if r.URL.Query().Get("force") != "1" {
			writeError(w, http.StatusConflict, "已有一份待生效的恢复,重启后生效;要覆盖它请加 ?force=1")
			return
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusInternalServerError, "检查暂存文件失败")
		return
	}

	// 校验(暂存之前):坏输入不落盘。校验通过会返回一份已迁移到当前 schema 的
	// 快照文件,写暂存的就是它。
	snapPath, _, err := backup.Validate(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	defer os.Remove(snapPath) // 校验用的临时快照,不留下

	// 校验通过才写暂存文件:先写临时名再改名,写一半挂了不会留下半个 .restore。
	src, err := os.Open(snapPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "打开校验过的快照失败")
		return
	}
	defer src.Close()

	tmp, err := os.CreateTemp(filepath.Dir(path), ".mpx-restore-*.tmp")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "创建暂存临时文件失败")
		return
	}
	tmpName := tmp.Name()
	_ = tmp.Chmod(0o600)
	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		writeError(w, http.StatusInternalServerError, "写入暂存文件失败")
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		writeError(w, http.StatusInternalServerError, "关闭暂存文件失败")
		return
	}
	if err := os.Rename(tmpName, stagedPath); err != nil {
		os.Remove(tmpName)
		writeError(w, http.StatusInternalServerError, "暂存文件就位失败")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"message": "已就绪,重启 mpx 后生效",
	})
}
