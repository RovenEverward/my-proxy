package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/control"
	"github.com/RovenEverward/my-proxy/internal/diagnostics"
	"github.com/RovenEverward/my-proxy/internal/model"
)

// jobEnvelope 是 POST .../jobs 的回答;reused 只在复用在跑的任务时出现。
type jobEnvelope struct {
	Job    model.Job `json:"job"`
	Reused bool      `json:"reused"`
}

// blockingCheckup 是一份会卡住的体检:第一次进来就等 release 关上,这样测试能
// 看到"接口已经回了、任务还在跑"的那一刻。
type blockingCheckup struct {
	fakeNodeOps
	started chan struct{}
	release chan struct{}
	err     error
	once    sync.Once
}

func newBlockingCheckup() *blockingCheckup {
	return &blockingCheckup{started: make(chan struct{}), release: make(chan struct{})}
}

func (b *blockingCheckup) CheckupNode(ctx context.Context, n model.Node, cfg diagnostics.Config, now time.Time) (model.DiagnosticRecord, error) {
	b.once.Do(func() { close(b.started) })
	// 不理会 ctx:关掉页面也不该把后台体检打断 —— 这正是这条接口存在的理由。
	<-b.release
	if b.err != nil {
		return model.DiagnosticRecord{}, b.err
	}
	return model.DiagnosticRecord{
		NodeID: n.ID, Source: "node", CheckedAt: now,
		Report: json.RawMessage(`{"position":"node","verdict":"ok"}`),
	}, nil
}

// waitJob 轮询到任务离开 running 或超时。
func (e *env) waitJob(id int64) model.Job {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		code, data := e.api("GET", fmt.Sprintf("/api/jobs/%d", id), nil)
		if code != http.StatusOK {
			e.t.Fatalf("读任务: got %d %s", code, data)
		}
		job := decodeJSON[model.Job](e.t, data)
		if job.Status != model.JobStatusRunning {
			return job
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatalf("任务 %d 一直没结束", id)
	return model.Job{}
}

func TestDiagnoseJobRunsInBackgroundAndReuses(t *testing.T) {
	ops := newBlockingCheckup()
	e := newEnvWith(t, ops)
	n := e.createNode("hk", "node.example.com")
	path := fmt.Sprintf("/api/nodes/%d/diagnostics/jobs", n.ID)

	code, data := e.api("POST", path, map[string]any{"network_only": false})
	if code != http.StatusAccepted {
		t.Fatalf("新任务该回 202,得到 %d %s", code, data)
	}
	first := decodeJSON[jobEnvelope](t, data)
	if first.Reused || first.Job.Status != model.JobStatusRunning || first.Job.Kind != model.JobKindDiagnose || first.Job.NodeID != n.ID {
		t.Fatalf("新任务不对: %+v", first)
	}
	<-ops.started

	// 还没跑完,再点一次:回 200、同一件,不起第二份。
	code, data = e.api("POST", path, map[string]any{"network_only": false})
	if code != http.StatusOK {
		t.Fatalf("已在跑时该回 200,得到 %d %s", code, data)
	}
	again := decodeJSON[jobEnvelope](t, data)
	if !again.Reused || again.Job.ID != first.Job.ID {
		t.Fatalf("该复用 %d,得到 %+v", first.Job.ID, again)
	}

	// 列表里能看到它正在跑。
	code, data = e.api("GET", fmt.Sprintf("/api/nodes/%d/jobs?active=1", n.ID), nil)
	if code != http.StatusOK {
		t.Fatalf("列任务: got %d %s", code, data)
	}
	list := decodeJSON[struct {
		Jobs []model.Job `json:"jobs"`
	}](t, data)
	if len(list.Jobs) != 1 || list.Jobs[0].ID != first.Job.ID {
		t.Fatalf("在跑的任务列表不对: %s", data)
	}

	close(ops.release)
	done := e.waitJob(first.Job.ID)
	if done.Status != model.JobStatusDone || done.Code != http.StatusOK || done.FinishedAt == nil {
		t.Fatalf("跑完该是 done/200: %+v", done)
	}
	record := decodeJSON[model.DiagnosticRecord](t, done.Result)
	var report struct {
		Verdict string `json:"verdict"`
	}
	if err := json.Unmarshal(record.Report, &report); err != nil {
		t.Fatalf("结果里的报告解不开: %v\n%s", err, done.Result)
	}
	if record.NodeID != n.ID || record.Source != "node" || report.Verdict != "ok" {
		t.Fatalf("结果里没有体检报告: %s", done.Result)
	}
}

func TestDiagnoseJobBodyValidationMatchesSync(t *testing.T) {
	ops := newBlockingCheckup()
	e := newEnvWith(t, ops)
	n := e.createNode("hk", "node.example.com")
	path := fmt.Sprintf("/api/nodes/%d/diagnostics/jobs", n.ID)
	for _, body := range []string{
		`{"position":"local"}`, `{"socks_url":"socks5h://127.0.0.1:1080"}`, `{"engine":"fusion","position":"control"}`,
		`{"engine":"light","hardware":true}`, `{"abuse_ipdb_key":"secret"}`, `{} {}`, `null`,
	} {
		if code, raw := e.requestRaw("POST", path, []byte(body)); code != http.StatusBadRequest {
			t.Errorf("%s => %d %s", body, code, raw)
		}
	}
	if code, _ := e.api("POST", "/api/nodes/99999/diagnostics/jobs", map[string]any{}); code != http.StatusNotFound {
		t.Errorf("节点不存在该 404,得到 %d", code)
	}
	if jobs, err := e.st.ListNodeJobs(n.ID, "", false, 0); err != nil || len(jobs) != 0 {
		t.Fatalf("不合法的请求不该留下任务: %+v err=%v", jobs, err)
	}

	legacy := newEnvWith(t, &fakeDiagnosticOps{})
	ln := legacy.createNode("legacy", "node.example.com")
	if code, _ := legacy.api("POST", fmt.Sprintf("/api/nodes/%d/diagnostics/jobs", ln.ID), map[string]any{}); code != http.StatusNotImplemented {
		t.Errorf("没接上体检该 501,得到 %d", code)
	}
}

func TestDiagnoseJobBusyIsFailedWithConflict(t *testing.T) {
	ops := &fakeCheckupOps{fakeDiagnosticOps: fakeDiagnosticOps{err: fmt.Errorf("wrapped: %w", control.ErrDiagnosticBusy)}}
	e := newEnvWith(t, ops)
	n := e.createNode("hk", "node.example.com")
	code, data := e.api("POST", fmt.Sprintf("/api/nodes/%d/diagnostics/jobs", n.ID), map[string]any{})
	if code != http.StatusAccepted {
		t.Fatalf("受理该回 202,得到 %d %s", code, data)
	}
	job := e.waitJob(decodeJSON[jobEnvelope](t, data).Job.ID)
	if job.Status != model.JobStatusFailed || job.Code != http.StatusConflict || !strings.Contains(job.Error, "wrapped") {
		t.Fatalf("该是 failed/409: %+v", job)
	}
	var view struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(job.Result, &view); err != nil || view.Error != job.Error {
		t.Fatalf("结果里该是同步接口的错误正文: %s", job.Result)
	}
}

func TestInstallJobSuccessAndFailure(t *testing.T) {
	t.Run("成功", func(t *testing.T) {
		ops := &fakeNodeOps{version: "v26.3.27"}
		e := newEnvWith(t, ops)
		n := e.createNode("hk", "node.example.com")
		code, data := e.api("POST", fmt.Sprintf("/api/nodes/%d/install/jobs?version=v26.3.27", n.ID), nil)
		if code != http.StatusAccepted {
			t.Fatalf("受理该回 202,得到 %d %s", code, data)
		}
		job := e.waitJob(decodeJSON[jobEnvelope](t, data).Job.ID)
		if job.Status != model.JobStatusDone || job.Code != http.StatusOK || ops.gotVersion != "v26.3.27" {
			t.Fatalf("安装任务不对: %+v got=%q", job, ops.gotVersion)
		}
		res := decodeJSON[map[string]string](t, job.Result)
		if res["version"] != "v26.3.27" || res["node"] != n.Name {
			t.Fatalf("结果里该有安装结果: %s", job.Result)
		}
		// 安装成功同同步接口一样清掉「有改动未应用」。
		if list, err := e.st.ListNodePending(); err != nil || len(list) != 0 {
			t.Fatalf("未应用标记没清: %+v err=%v", list, err)
		}
	})
	t.Run("失败", func(t *testing.T) {
		ops := &fakeNodeOps{version: "v26.3.27", err: errors.New("配置通过了校验但服务没起来")}
		e := newEnvWith(t, ops)
		n := e.createNode("hk", "node.example.com")
		code, data := e.api("POST", fmt.Sprintf("/api/nodes/%d/install/jobs", n.ID), nil)
		if code != http.StatusAccepted {
			t.Fatalf("受理该回 202,得到 %d %s", code, data)
		}
		job := e.waitJob(decodeJSON[jobEnvelope](t, data).Job.ID)
		if job.Status != model.JobStatusFailed || job.Code != http.StatusBadGateway ||
			!strings.Contains(job.Error, "v26.3.27") || !strings.Contains(job.Error, "服务没起来") {
			t.Fatalf("安装失败的任务不对: %+v", job)
		}
	})
}

// installJobOps 是一份永远不返回的安装:让复用那条分支有机会被看到。
type installJobOps struct {
	fakeNodeOps
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (o *installJobOps) InstallNode(ctx context.Context, n model.Node, version string) (string, error) {
	o.once.Do(func() { close(o.started) })
	<-o.release
	return o.fakeNodeOps.InstallNode(ctx, n, version)
}

func TestInstallJobReusesRunningInstall(t *testing.T) {
	ops := &installJobOps{started: make(chan struct{}), release: make(chan struct{})}
	e := newEnvWith(t, ops)
	n := e.createNode("hk", "node.example.com")
	path := fmt.Sprintf("/api/nodes/%d/install/jobs", n.ID)
	code, data := e.api("POST", path, nil)
	if code != http.StatusAccepted {
		t.Fatalf("新任务该回 202,得到 %d %s", code, data)
	}
	first := decodeJSON[jobEnvelope](t, data)
	<-ops.started
	code, data = e.api("POST", path+"?core=xray", nil)
	if code != http.StatusOK || !decodeJSON[jobEnvelope](t, data).Reused {
		t.Fatalf("装到一半再点该回 200 复用,得到 %d %s", code, data)
	}
	if code, _ := e.api("POST", path+"?core=v2ray", nil); code != http.StatusBadRequest {
		t.Fatalf("核心名不合法该回 400,得到 %d", code)
	}
	close(ops.release)
	if job := e.waitJob(first.Job.ID); job.Status != model.JobStatusDone {
		t.Fatalf("跑完该是 done: %+v", job)
	}
}

func TestInstallJobPortConflictCreatesNoJob(t *testing.T) {
	ops := &portOps{fakeNodeOps: fakeNodeOps{version: "v26.3.27"}, tcp: map[int]string{443: "nginx(进程 1234)"}}
	e := newEnvWith(t, ops)
	n := e.createNode("hk", "node.example.com")
	e.createInbound(n.ID, "", 443)

	code, data := e.api("POST", fmt.Sprintf("/api/nodes/%d/install/jobs", n.ID), nil)
	if code != http.StatusConflict {
		t.Fatalf("端口被占该回 409,得到 %d %s", code, data)
	}
	for _, want := range []string{"443 端口(TCP)已被 nginx(进程 1234)", "ui_hint", "入站与入口"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("响应缺 %q: %s", want, data)
		}
	}
	if jobs, err := e.st.ListNodeJobs(n.ID, "", false, 0); err != nil || len(jobs) != 0 {
		t.Fatalf("端口冲突不该留下任务: %+v err=%v", jobs, err)
	}
	if ops.installs != 0 {
		t.Error("端口被占时不该开始装核心")
	}
}

func TestInstallJobValidationMatchesSync(t *testing.T) {
	e := newEnvWith(t, &fakeNodeOps{})
	if code, _ := e.api("POST", "/api/nodes/999/install/jobs", nil); code != http.StatusNotFound {
		t.Fatalf("节点不存在该 404,得到 %d", code)
	}
	noOps := newEnv(t)
	n := noOps.createNode("hk", "node.example.com")
	if code, _ := noOps.api("POST", fmt.Sprintf("/api/nodes/%d/install/jobs", n.ID), nil); code != http.StatusNotImplemented {
		t.Fatalf("没接上安装通道该 501,得到 %d", code)
	}
}

func TestJobsEndpointsReadAndValidate(t *testing.T) {
	e := newEnv(t)
	if code, _ := e.api("GET", "/api/jobs/1", nil); code != http.StatusNotFound {
		t.Fatalf("不存在的任务该 404,得到 %d", code)
	}
	if code, _ := e.api("GET", "/api/jobs/no", nil); code != http.StatusBadRequest {
		t.Fatalf("非数字 id 该 400,得到 %d", code)
	}
	if code, _ := e.request("GET", "/api/jobs/1", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("不带 token 该 401,得到 %d", code)
	}

	// 直接落几件任务,验列表的默认值、过滤与参数校验。
	n := e.createNode("hk", "node.example.com")
	for i := 0; i < 7; i++ {
		job, _, err := e.st.StartJob(model.JobKindDiagnose, n.ID, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err := e.st.FinishJob(job.ID, model.JobStatusDone, 200, []byte(`{}`), "", "", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if job, _, err := e.st.StartJob(model.JobKindInstall, n.ID, time.Now()); err != nil || job.ID == 0 {
		t.Fatalf("起安装任务: %+v err=%v", job, err)
	}

	type jobList struct {
		Jobs []model.Job `json:"jobs"`
	}
	listPath := fmt.Sprintf("/api/nodes/%d/jobs", n.ID)
	code, data := e.api("GET", listPath, nil)
	if code != http.StatusOK {
		t.Fatalf("列任务: got %d %s", code, data)
	}
	if l := decodeJSON[jobList](t, data); len(l.Jobs) != 5 || l.Jobs[0].Kind != model.JobKindInstall {
		t.Fatalf("默认该回最新 5 件: %s", data)
	}
	code, data = e.api("GET", listPath+"?kind=diagnose&limit=3", nil)
	if l := decodeJSON[jobList](t, data); code != http.StatusOK || len(l.Jobs) != 3 {
		t.Fatalf("按种类取 3 件: %d %s", code, data)
	}
	// 库里就这 8 件,limit 超过上限按 20 处理,回的就是全部 8 件。
	code, data = e.api("GET", listPath+"?limit=100", nil)
	if l := decodeJSON[jobList](t, data); code != http.StatusOK || len(l.Jobs) != 8 {
		t.Fatalf("limit 超过上限该按 20 截: %d %s", code, data)
	}
	code, data = e.api("GET", listPath+"?active=1", nil)
	if l := decodeJSON[jobList](t, data); code != http.StatusOK || len(l.Jobs) != 1 || l.Jobs[0].Status != model.JobStatusRunning {
		t.Fatalf("只看在跑的: %d %s", code, data)
	}
	for _, q := range []string{"?kind=other", "?active=yes", "?limit=0", "?limit=abc"} {
		if code, _ := e.api("GET", listPath+q, nil); code != http.StatusBadRequest {
			t.Errorf("%s 该 400,得到 %d", q, code)
		}
	}
	if code, _ := e.api("GET", "/api/nodes/99999/jobs", nil); code != http.StatusNotFound {
		t.Errorf("节点不存在该 404,得到 %d", code)
	}
}
