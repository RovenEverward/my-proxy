package store

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

func TestStartJobReusesRunningJob(t *testing.T) {
	s := newTestStore(t)
	n := newTestNode(t, s, "hk")
	at := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

	job, reused, err := s.StartJob(model.JobKindDiagnose, n.ID, at)
	if err != nil {
		t.Fatal(err)
	}
	if reused || job.ID == 0 || job.Status != model.JobStatusRunning || job.Kind != model.JobKindDiagnose ||
		job.NodeID != n.ID || !job.StartedAt.Equal(at) || job.FinishedAt != nil {
		t.Fatalf("新任务不对: %+v reused=%v", job, reused)
	}

	// 同一节点同一类任务再开一次:复用,不起第二份。
	again, reused, err := s.StartJob(model.JobKindDiagnose, n.ID, at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !reused || again.ID != job.ID {
		t.Fatalf("该复用 %d,得到 %+v reused=%v", job.ID, again, reused)
	}

	// 别的种类、别的节点各算各的。
	if other, reused, err := s.StartJob(model.JobKindInstall, n.ID, at); err != nil || reused || other.ID == job.ID {
		t.Fatalf("安装任务不该被体检挡住: %+v reused=%v err=%v", other, reused, err)
	}
	m := newTestNode(t, s, "us")
	if other, reused, err := s.StartJob(model.JobKindDiagnose, m.ID, at); err != nil || reused || other.ID == job.ID {
		t.Fatalf("别的节点不该被挡住: %+v reused=%v err=%v", other, reused, err)
	}

	// 前一件结束后再开,是新的。
	if err := s.FinishJob(job.ID, model.JobStatusDone, 200, []byte(`{"ok":true}`), "", "", at); err != nil {
		t.Fatal(err)
	}
	fresh, reused, err := s.StartJob(model.JobKindDiagnose, n.ID, at)
	if err != nil || reused || fresh.ID == job.ID {
		t.Fatalf("上一件结束后应当新起一件: %+v reused=%v err=%v", fresh, reused, err)
	}
}

func TestFinishJobRecordsOutcome(t *testing.T) {
	s := newTestStore(t)
	n := newTestNode(t, s, "hk")
	start := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	job, _, err := s.StartJob(model.JobKindDiagnose, n.ID, start)
	if err != nil {
		t.Fatal(err)
	}
	end := start.Add(2 * time.Minute)
	result := json.RawMessage(`{"position":"node"}`)
	if err := s.FinishJob(job.ID, model.JobStatusFailed, 409, result, "节点正在体检", "稍后再试", end); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.JobStatusFailed || got.Code != 409 || got.Error != "节点正在体检" || got.UIHint != "稍后再试" ||
		string(got.Result) != string(result) || got.FinishedAt == nil || !got.FinishedAt.Equal(end) {
		t.Fatalf("结局不对: %+v", got)
	}

	// 已经有结局了,迟到的结果不再覆盖。
	if err := s.FinishJob(job.ID, model.JobStatusDone, 200, []byte(`{}`), "", "", end); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.JobStatusFailed || got.Code != 409 {
		t.Fatalf("结局被覆盖了: %+v", got)
	}

	if _, err := s.GetJob(99999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在的任务该是 ErrNotFound,得到 %v", err)
	}
	if err := s.FinishJob(99999, model.JobStatusDone, 200, nil, "", "", end); !errors.Is(err, ErrNotFound) {
		t.Fatalf("给不存在的任务写结局该是 ErrNotFound,得到 %v", err)
	}
}

func TestFinishJobKeepsNewestTwentyFinished(t *testing.T) {
	s := newTestStore(t)
	n := newTestNode(t, s, "hk")
	at := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

	// 先留一件在跑:它不算在保留数里,也不该被清掉。换成安装任务是为了不挡住
	// 下面连开的体检(同一节点同一类任务在跑时会被复用)。
	pending, _, err := s.StartJob(model.JobKindInstall, n.ID, at)
	if err != nil {
		t.Fatal(err)
	}

	var ids []int64
	for i := 0; i < 25; i++ {
		job, reused, err := s.StartJob(model.JobKindDiagnose, n.ID, at)
		if err != nil || reused {
			t.Fatalf("第 %d 件: %+v reused=%v err=%v", i, job, reused, err)
		}
		ids = append(ids, job.ID)
		if err := s.FinishJob(job.ID, model.JobStatusDone, 200, []byte(`{}`), "", "", at); err != nil {
			t.Fatal(err)
		}
	}

	all, err := s.ListNodeJobs(n.ID, model.JobKindDiagnose, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != jobKeepFinished {
		t.Fatalf("应当留下 %d 件已结束的,实际 %d", jobKeepFinished, len(all))
	}
	// 留下的是最新的那些。
	for i := 0; i < jobKeepFinished; i++ {
		want := ids[len(ids)-1-i]
		if all[i].ID != want || all[i].Status != model.JobStatusDone {
			t.Fatalf("第 %d 件该是 %d,实际 %d", i, want, all[i].ID)
		}
	}
	if got, err := s.GetJob(pending.ID); err != nil || got.Status != model.JobStatusRunning || got.ID != pending.ID {
		t.Fatalf("在跑的安装任务被清了: %+v err=%v", got, err)
	}

	// 别的种类不受影响:另起一批安装任务,体检的保留不受牵连。
	for i := 0; i < jobKeepFinished+2; i++ {
		job, _, err := s.StartJob(model.JobKindInstall, n.ID, at)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.FinishJob(job.ID, model.JobStatusDone, 200, nil, "", "", at); err != nil {
			t.Fatal(err)
		}
	}
	diagnoses, err := s.ListNodeJobs(n.ID, model.JobKindDiagnose, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(diagnoses) != jobKeepFinished {
		t.Fatalf("安装任务的清理动了体检任务: %d", len(diagnoses))
	}
}

func TestListNodeJobsFiltersAndOrders(t *testing.T) {
	s := newTestStore(t)
	n := newTestNode(t, s, "hk")
	other := newTestNode(t, s, "us")
	at := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

	first, _, _ := s.StartJob(model.JobKindDiagnose, n.ID, at)
	if err := s.FinishJob(first.ID, model.JobStatusDone, 200, nil, "", "", at); err != nil {
		t.Fatal(err)
	}
	second, _, err := s.StartJob(model.JobKindInstall, n.ID, at)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.StartJob(model.JobKindDiagnose, n.ID, at); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.StartJob(model.JobKindInstall, other.ID, at); err != nil {
		t.Fatal(err)
	}

	all, err := s.ListNodeJobs(n.ID, "", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].Kind != model.JobKindDiagnose || all[0].ID <= second.ID || all[2].ID != first.ID {
		t.Fatalf("该按最新在前列出 3 件: %+v", all)
	}
	installs, err := s.ListNodeJobs(n.ID, model.JobKindInstall, false, 0)
	if err != nil || len(installs) != 1 || installs[0].ID != second.ID {
		t.Fatalf("按种类过滤不对: %+v err=%v", installs, err)
	}
	active, err := s.ListNodeJobs(n.ID, "", true, 0)
	if err != nil || len(active) != 2 {
		t.Fatalf("只看在跑的该有 2 件: %+v err=%v", active, err)
	}
	limited, err := s.ListNodeJobs(n.ID, "", false, 1)
	if err != nil || len(limited) != 1 || limited[0].ID != all[0].ID {
		t.Fatalf("limit 不对: %+v err=%v", limited, err)
	}
}

func TestInterruptRunningJobs(t *testing.T) {
	s := newTestStore(t)
	n := newTestNode(t, s, "hk")
	at := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

	done, _, err := s.StartJob(model.JobKindDiagnose, n.ID, at)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishJob(done.ID, model.JobStatusDone, 200, []byte(`{"ok":1}`), "", "", at); err != nil {
		t.Fatal(err)
	}
	running, _, err := s.StartJob(model.JobKindInstall, n.ID, at)
	if err != nil {
		t.Fatal(err)
	}

	restartAt := at.Add(time.Hour)
	count, err := s.InterruptRunningJobs(restartAt)
	if err != nil || count != 1 {
		t.Fatalf("该标 1 件,得到 %d err=%v", count, err)
	}
	got, err := s.GetJob(running.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.JobStatusInterrupted || got.Error != "控制面重启了，这次没做完" ||
		got.FinishedAt == nil || !got.FinishedAt.Equal(restartAt) {
		t.Fatalf("中断的那件不对: %+v", got)
	}
	survivor, err := s.GetJob(done.ID)
	if err != nil {
		t.Fatal(err)
	}
	if survivor.Status != model.JobStatusDone || survivor.Code != 200 || string(survivor.Result) != `{"ok":1}` {
		t.Fatalf("已结束的那件被动了: %+v", survivor)
	}

	// 再调一次没有可标的。
	if count, err := s.InterruptRunningJobs(restartAt); err != nil || count != 0 {
		t.Fatalf("第二次该标 0 件,得到 %d err=%v", count, err)
	}
	// 中断之后同名任务可以重新开始。
	if job, reused, err := s.StartJob(model.JobKindInstall, n.ID, at); err != nil || reused || job.ID == running.ID {
		t.Fatalf("中断后该能重新开始: %+v reused=%v err=%v", job, reused, err)
	}
}

func TestDeleteNodeDropsJobs(t *testing.T) {
	s := newTestStore(t)
	n := newTestNode(t, s, "hk")
	job, _, err := s.StartJob(model.JobKindDiagnose, n.ID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteNode(n.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetJob(job.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("节点删掉后任务应当跟着走,得到 %v", err)
	}
}
