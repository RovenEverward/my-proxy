package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// 后台任务:体检、安装这类要跑十几分钟的事,由控制面在后台替节点做完,记在这里。
// 关掉页面不会影响它;控制面重启时没做完的那些在启动时标成「中断」。
//
// result 存的是同步接口会回的 JSON 正文原样文本,空文本表示还没有正文。时间存
// Unix 秒,finished_at 可空 —— 没跑完就没有那一刻。
func (s *Store) migrateJobs() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS node_jobs (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		kind        TEXT    NOT NULL,
		node_id     INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
		status      TEXT    NOT NULL,
		result      TEXT    NOT NULL DEFAULT '',
		error       TEXT    NOT NULL DEFAULT '',
		ui_hint     TEXT    NOT NULL DEFAULT '',
		code        INTEGER NOT NULL DEFAULT 0,
		started_at  INTEGER NOT NULL,
		finished_at INTEGER
	)`); err != nil {
		return fmt.Errorf("建 node_jobs 表: %w", err)
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_node_jobs_node_kind ON node_jobs(node_id, kind, id)`); err != nil {
		return fmt.Errorf("建 node_jobs 索引: %w", err)
	}
	return nil
}

// jobColumns 是 jobSelect 读出来的列,顺序与 scanJob 一致。
const jobSelect = `SELECT id, kind, node_id, status, started_at, finished_at, code, error, ui_hint, result FROM node_jobs`

func scanJob(sc rowScanner) (*model.Job, error) {
	var j model.Job
	var started int64
	var finished sql.NullInt64
	var raw string
	if err := sc.Scan(&j.ID, &j.Kind, &j.NodeID, &j.Status, &started, &finished, &j.Code, &j.Error, &j.UIHint, &raw); err != nil {
		return nil, err
	}
	j.StartedAt = time.Unix(started, 0).UTC()
	if finished.Valid {
		at := time.Unix(finished.Int64, 0).UTC()
		j.FinishedAt = &at
	}
	if raw != "" {
		j.Result = json.RawMessage(raw)
	}
	return &j, nil
}

// StartJob 为这台节点开一件后台任务。同一节点同一类任务已有在跑的,直接把它还回
// 来并置 reused —— 重复点「体检」不该再起一份,两路 SSH 同时跑还会互相挤。
//
// 查与插在同一个事务里完成:两个同时到达的请求里,后一个要么看到前一个插进去的
// 那件、复用,要么在它提交之后才拿到连接,同样看到它。
func (s *Store) StartJob(kind string, nodeID int64, at time.Time) (job model.Job, reused bool, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return model.Job{}, false, fmt.Errorf("开始记录后台任务: %w", err)
	}
	defer tx.Rollback()

	existing, err := scanJob(tx.QueryRow(jobSelect+` WHERE node_id=? AND kind=? AND status=? ORDER BY id DESC LIMIT 1`,
		nodeID, kind, model.JobStatusRunning))
	switch {
	case err == nil:
		return *existing, true, nil
	case !errors.Is(err, sql.ErrNoRows):
		return model.Job{}, false, fmt.Errorf("查询节点 %d 在跑的后台任务: %w", nodeID, err)
	}

	res, err := tx.Exec(`INSERT INTO node_jobs(kind, node_id, status, started_at) VALUES(?,?,?,?)`,
		kind, nodeID, model.JobStatusRunning, normalizeTime(at).Unix())
	if err != nil {
		return model.Job{}, false, fmt.Errorf("记录节点 %d 的后台任务: %w", nodeID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return model.Job{}, false, fmt.Errorf("读取后台任务 id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return model.Job{}, false, fmt.Errorf("提交后台任务: %w", err)
	}
	return model.Job{
		ID: id, Kind: kind, NodeID: nodeID, Status: model.JobStatusRunning,
		StartedAt: normalizeTime(at),
	}, false, nil
}

// jobKeepFinished 是每台节点每类任务保留的已结束条数,超出的删掉最早的那几条。
const jobKeepFinished = 20

// FinishJob 给一件还在跑的任务写下结局。只改仍为 running 的行:一件任务已经有
// 结局了(例如控制面重启时被标成中断)就不再被迟到的结果覆盖。
//
// 写完顺手把同一节点同一类任务里超出保留数的旧记录删掉 —— 任务只是最近几次操作
// 的记录,不是历史档案。
func (s *Store) FinishJob(id int64, status string, code int, result []byte, errMsg, hint string, at time.Time) error {
	if status == model.JobStatusRunning {
		return fmt.Errorf("后台任务 %d 的结局不能还是「在跑」", id)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("记录后台任务 %d 的结局: %w", id, err)
	}
	defer tx.Rollback()

	// 先记下这件任务属于哪台节点、哪一类 —— 清理旧记录时要按它来。
	var kind string
	var nodeID int64
	if err := tx.QueryRow(`SELECT kind, node_id FROM node_jobs WHERE id=?`, id).Scan(&kind, &nodeID); err != nil {
		return fmt.Errorf("记录后台任务 %d 的结局: %w", id, wrapNotFound(err, fmt.Sprintf("后台任务 %d", id)))
	}

	if _, err := tx.Exec(`UPDATE node_jobs SET status=?, code=?, result=?, error=?, ui_hint=?, finished_at=?
		WHERE id=? AND status=?`,
		status, code, string(result), errMsg, hint, normalizeTime(at).Unix(), id, model.JobStatusRunning); err != nil {
		return fmt.Errorf("记录后台任务 %d 的结局: %w", id, err)
	}

	if _, err := tx.Exec(`DELETE FROM node_jobs
		WHERE node_id=? AND kind=? AND status != ?
		  AND id NOT IN (SELECT id FROM node_jobs WHERE node_id=? AND kind=? AND status != ?
			ORDER BY id DESC LIMIT ?)`,
		nodeID, kind, model.JobStatusRunning, nodeID, kind, model.JobStatusRunning, jobKeepFinished); err != nil {
		return fmt.Errorf("清理后台任务 %d 的旧记录: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交后台任务 %d 的结局: %w", id, err)
	}
	return nil
}

// GetJob 读一件后台任务,不存在时返回 ErrNotFound。
func (s *Store) GetJob(id int64) (*model.Job, error) {
	j, err := scanJob(s.db.QueryRow(jobSelect+` WHERE id=?`, id))
	if err != nil {
		return nil, wrapNotFound(err, fmt.Sprintf("后台任务 %d", id))
	}
	return j, nil
}

// ListNodeJobs 列出这台节点的后台任务,最新的在前。kind 为空表示所有种类;
// activeOnly 只看在跑的;limit <= 0 表示不限条数。
func (s *Store) ListNodeJobs(nodeID int64, kind string, activeOnly bool, limit int) ([]model.Job, error) {
	q := jobSelect + ` WHERE node_id=?`
	args := []any{nodeID}
	if kind != "" {
		q += ` AND kind=?`
		args = append(args, kind)
	}
	if activeOnly {
		q += ` AND status=?`
		args = append(args, model.JobStatusRunning)
	}
	q += ` ORDER BY id DESC`
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("读取节点 %d 的后台任务: %w", nodeID, err)
	}
	defer rows.Close()
	out := []model.Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("读取节点 %d 的后台任务: %w", nodeID, err)
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}

// jobInterruptedMessage 是控制面重启时给没跑完的任务留下的说法。
const jobInterruptedMessage = "控制面重启了，这次没做完"

// InterruptRunningJobs 把还标着在跑的任务都改成中断。控制面启动时调一次:上次
// 进程没了,那些任务不可能再有结局,留着「在跑」会挡住同名任务重新开始。
// 返回改动了几件。
func (s *Store) InterruptRunningJobs(at time.Time) (int, error) {
	res, err := s.db.Exec(`UPDATE node_jobs SET status=?, error=?, finished_at=? WHERE status=?`,
		model.JobStatusInterrupted, jobInterruptedMessage, normalizeTime(at).Unix(), model.JobStatusRunning)
	if err != nil {
		return 0, fmt.Errorf("把没做完的后台任务标成中断: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("读取标成中断的后台任务数: %w", err)
	}
	return int(n), nil
}
