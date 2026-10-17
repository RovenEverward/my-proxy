package ui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	setupMaxDelta  = 4096
	setupKeepLines = 500
	setupLineMax   = 300
	setupEllipsis  = "…"
)

// SetupParams 是首次安装向导表单交上来的参数。Password 只在 Check 里用一次。
type SetupParams struct {
	Host           string `json:"host"`
	SSHPort        int    `json:"ssh_port"`
	SSHUser        string `json:"ssh_user"`
	Password       string `json:"password,omitempty"`
	Identity       string `json:"identity,omitempty"`
	Name           string `json:"name"`
	Username       string `json:"username"`
	DevicePassword string `json:"device_password,omitempty"`
	Addr           string `json:"addr,omitempty"`
	SubPrefix      string `json:"sub_prefix,omitempty"`
	SNI            string `json:"sni,omitempty"`
	// InboundPort:第一个入站的端口,0 = 默认 443(被占用时自动换)。v0.9.5 新增。
	InboundPort int `json:"inbound_port,omitempty"`
}

// SetupProgress 是 runner 汇报进度的口子。
type SetupProgress interface {
	Step(title string) // 进入新的一步(上一步自动记为完成)
	Line(text string)  // 一行输出(远端命令的 stdout/stderr、提示)
}

// SetupResult 是装完之后页面要显示、以及 mpx ui 切换到正常模式要用的东西。
type SetupResult struct {
	HostPort    string            `json:"host_port"`
	Fingerprint string            `json:"fingerprint"`
	Connected   bool              `json:"connected"`
	Username    string            `json:"username"`
	Upstream    *url.URL          `json:"-"`
	Token       string            `json:"-"`
	Transport   http.RoundTripper `json:"-"`
	Profile     string            `json:"-"`
}

// SetupRunner 由 cli 实现(调 runInitSteps),测试里换成假的。
type SetupRunner interface {
	Check(ctx context.Context, p SetupParams) (map[string]string, error) // 用密码装公钥 + 探路,返回 os/arch/uid/fetch
	Run(ctx context.Context, p SetupParams, prog SetupProgress) (*SetupResult, error)
}

// ConnectRunner 由 cli 实现(等价 mpx connect),测试里换成假的。指纹先经 Probe
// 取回、交页面上的人核对,确认过的指纹再随 Connect 送去换 token —— 与终端里
// 「先摆指纹、输入 yes 才继续」是同一道手续。
type ConnectRunner interface {
	// Probe 整理地址并取回证书指纹:addr 是人填的(IP / 域名 / https://host:port),
	// server 是归一后的 https://host:port,fingerprint 形如 SHA256:…。
	Probe(ctx context.Context, addr string) (server, fingerprint string, err error)
	// Connect 用设备密码换 token、存控制面并切为当前,返回能让页面切到正常模式的
	// Upstream/Token/Transport/Profile。name 为空由实现方取默认(主机名)。
	Connect(ctx context.Context, server, fingerprint, password, name string) (*SetupResult, error)
}

// ConnectNamedRunner 是 ConnectRunner 的可选扩展:接入结果存进**指定名字的新
// 控制面**而不是实现方自己挑的那个。mpx ui 正常模式的「添加控制面」走它
// (POST /local/profiles/connect);只实现了 ConnectRunner 的旧实现照常工作,只是
// 那两个接口回 404。
type ConnectNamedRunner interface {
	ConnectRunner
	// ConnectProfile 与 Connect 同一件事,但必须存成名为 profile 的**新**控制面:
	// 档已存在时报错,绝不覆盖(默认的 default 档也不行)。name 仍是设备名。
	ConnectProfile(ctx context.Context, server, fingerprint, password, name, profile string) (*SetupResult, error)
}

// setupStep 是一步的状态。State 取 run|done|fail。
type setupStep struct {
	Title string  `json:"title"`
	State string  `json:"state"`
	Secs  float64 `json:"secs"`
}

// setupLine 是环形缓冲里的一行输出,Seq 是它加入时的变化序号。
type setupLine struct {
	Seq  int64
	Text string
}

// setupJob 是「首次安装任务」的并发安全状态。
type setupJob struct {
	mu       sync.Mutex
	id       string
	steps    []setupStep
	stepsSeq int64 // steps 最后一次变化时的 seq
	lines    []setupLine
	seq      int64
	stepAt   time.Time
	state    string
	errMsg   string
	result   *SetupResult
	started  time.Time
}

// newSetupJob 建一个还在 running 的任务,id 是随机 8 字节的 hex。
func newSetupJob() *setupJob {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand 读不到时退化成一个仍然唯一性够用的值。
		return &setupJob{
			id:      hex.EncodeToString([]byte(time.Now().Format("150405.000000"))),
			state:   "running",
			started: time.Now(),
		}
	}
	return &setupJob{
		id:      hex.EncodeToString(b[:]),
		state:   "running",
		started: time.Now(),
	}
}

// Step 进入新的一步:上一步若仍在 run 就记为 done 并写上耗时。
func (j *setupJob) Step(title string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.seq++
	j.closeRunningStep("done")
	j.steps = append(j.steps, setupStep{Title: title, State: "run"})
	j.stepAt = time.Now()
	j.stepsSeq = j.seq
}

// Line 按 \n 拆行,逐行写入环形缓冲;单行过长先按 rune 边界截断再加 "…"。
func (j *setupJob) Line(text string) {
	parts := strings.Split(text, "\n")
	if n := len(parts); n > 0 && parts[n-1] == "" {
		parts = parts[:n-1]
	}
	if len(parts) == 0 {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, p := range parts {
		j.seq++
		j.lines = append(j.lines, setupLine{Seq: j.seq, Text: truncateSetupLine(p)})
	}
	if n := len(j.lines); n > setupKeepLines {
		j.lines = append([]setupLine(nil), j.lines[n-setupKeepLines:]...)
	}
}

// finish 收尾:err 非空把当前 run 步置 fail 并记错误,否则置 done 并保存结果。
func (j *setupJob) finish(res *SetupResult, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.seq++
	if err != nil {
		j.closeRunningStep("fail")
		j.state = "failed"
		j.errMsg = err.Error()
	} else {
		j.closeRunningStep("done")
		j.state = "done"
		j.result = res
	}
	j.stepsSeq = j.seq
}

// closeRunningStep 在持锁状态下给最后一步收尾。st 取 done 或 fail。
func (j *setupJob) closeRunningStep(st string) {
	if n := len(j.steps); n > 0 && j.steps[n-1].State == "run" {
		j.steps[n-1].State = st
		if !j.stepAt.IsZero() {
			j.steps[n-1].Secs = time.Since(j.stepAt).Seconds()
		}
	}
}

// setupDelta 是给页面轮询的增量视图。
type setupDelta struct {
	ID      string       `json:"id"`
	Seq     int64        `json:"seq"` // 客户端下次带回的 since
	State   string       `json:"state"`
	Steps   []setupStep  `json:"steps,omitempty"` // 只在 since 之后步骤有变化(或 since=0)时给
	Lines   []string     `json:"lines,omitempty"` // since 之后的新行
	More    bool         `json:"more,omitempty"`  // 还有没给完的新行,客户端应立刻再拉
	Error   string       `json:"error,omitempty"`
	Result  *SetupResult `json:"result,omitempty"`
	Elapsed float64      `json:"elapsed"`
}

// delta 返回 since 之后的增量。since >= seq 时只给 ID/Seq/State/Elapsed。
func (j *setupJob) delta(since int64) setupDelta {
	j.mu.Lock()
	defer j.mu.Unlock()

	d := setupDelta{
		ID:      j.id,
		Seq:     j.seq,
		State:   j.state,
		Elapsed: time.Since(j.started).Seconds(),
	}
	if since >= j.seq {
		return d
	}

	if j.stepsSeq > since {
		d.Steps = append([]setupStep(nil), j.steps...)
	}
	d.Error = j.errMsg
	d.Result = j.result

	gave := false
	for _, ln := range j.lines {
		if ln.Seq <= since {
			continue
		}
		cand := d
		cand.Lines = append(append([]string(nil), d.Lines...), ln.Text)
		cand.More = true
		if setupDeltaSize(cand) >= setupMaxDelta {
			d.More = true
			break
		}
		d.Lines = append(d.Lines, ln.Text)
		d.Seq = ln.Seq
		gave = true
	}
	if d.More && !gave {
		// 一行都没给出去时不能前进 seq,否则会丢行;让客户端用同一个 since 重试。
		d.Seq = since
	} else if !d.More {
		// 全给完了:steps 与 lines 都已到最新,seq 前进到当前值。
		d.Seq = j.seq
	}
	return d
}

// setupDeltaSize 用 json.Marshal 估算增量序列化后的大小。
func setupDeltaSize(d setupDelta) int {
	b, err := json.Marshal(d)
	if err != nil {
		return setupMaxDelta
	}
	return len(b)
}

// truncateSetupLine 把一行截到 setupLineMax 字节以内(含省略号),且保证 UTF-8 合法。
func truncateSetupLine(s string) string {
	if len(s) <= setupLineMax {
		return s
	}
	limit := setupLineMax - len(setupEllipsis)
	b := []byte(s)
	if len(b) > limit {
		b = b[:limit]
	}
	for len(b) > 0 && !utf8.Valid(b) {
		b = b[:len(b)-1]
	}
	return string(b) + setupEllipsis
}
