package server

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/ruleset"
)

// rulesetRewriteTransport 把拉取请求的 host 改写到 httptest 上游 —— 真实目录里
// 全是 CDN 地址,测试不能出网。
type rulesetRewriteTransport struct{ target string }

func (t rulesetRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r2 := req.Clone(req.Context())
	r2.URL.Scheme = "http"
	r2.URL.Host = t.target
	return http.DefaultTransport.RoundTrip(r2)
}

// newRulesetEnv 装一个接了镜像拉取器的测试环境,上游指向本地 httptest。
func newRulesetEnv(t *testing.T, hits *int64) (*env, *ruleset.Manager) {
	t.Helper()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			atomic.AddInt64(hits, 1)
		}
		w.Header().Set("Content-Type", "text/yaml")
		_, _ = w.Write([]byte("payload: test"))
	}))
	t.Cleanup(up.Close)
	e := newEnv(t)
	mgr := ruleset.New(e.st,
		&http.Client{Transport: rulesetRewriteTransport{target: strings.TrimPrefix(up.URL, "http://")}},
		nil)
	mgr.SetRefs(e.s.listEnabledRules)
	e.s.SetRulesets(mgr)
	t.Cleanup(mgr.Close)
	return e, mgr
}

// putRules 给全局作用域整表种规则(version 0 起手)。
func (e *env) putRules(rules []model.RouteRule) {
	e.t.Helper()
	if code, b := e.api("PUT", "/api/rules", map[string]any{"rules": rules, "version": 0}); code != 200 {
		e.t.Fatalf("种规则: %d %s", code, b)
	}
}

// 镜像下载口:token、格式、名字的白名单与就绪语义。
func TestRulesetFileHandler(t *testing.T) {
	e, _ := newRulesetEnv(t, nil)
	_, subPath := e.createSubReadyUser("alice")

	// 拉取器没接的环境:一律 404(不暴露功能存在与否)。
	e2 := newEnv(t)
	_, p2 := e2.createSubReadyUser("bob")
	if _, err := e2.st.CompleteRulesetRefresh("clash/openai", "u", time.Now(), []byte("x"), "", ""); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := e2.sub(p2+"/ruleset/clash/openai", ""); code != http.StatusNotFound {
		t.Fatalf("未接拉取器应 404: %d", code)
	}

	if code, _, _ := e.sub("/sub/no-such-token/ruleset/clash/openai", ""); code != http.StatusNotFound {
		t.Fatalf("假 token 应 404: %d", code)
	}
	if code, _, _ := e.sub(subPath+"/ruleset/weird/openai", ""); code != http.StatusBadRequest {
		t.Fatalf("非法 fmt 应 400: %d", code)
	}
	if code, _, _ := e.sub(subPath+"/ruleset/clash/bad%20name", ""); code != http.StatusNotFound {
		t.Fatalf("非法 name 应 404: %d", code)
	}
	if code, _, _ := e.sub(subPath+"/ruleset/clash/openai", ""); code != http.StatusNotFound {
		t.Fatalf("未就绪的镜像应 404: %d", code)
	}

	if _, err := e.st.CompleteRulesetRefresh("clash/openai", "https://up/a.yaml",
		time.Now(), []byte("payload: 1"), "", ""); err != nil {
		t.Fatalf("写快照: %v", err)
	}
	code, hdr, body := e.sub(subPath+"/ruleset/clash/openai", "")
	if code != http.StatusOK || string(body) != "payload: 1" {
		t.Fatalf("下载镜像: %d %q", code, body)
	}
	if ct := hdr.Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("无后缀名兜底 Content-Type: %q", ct)
	}
	if hdr.Get("X-Content-Type-Options") != "nosniff" || hdr.Get("Cache-Control") == "" {
		t.Fatalf("缺安全/缓存头: %v", hdr)
	}

	// 上游给了 Content-Type 的按它走。
	if _, err := e.st.CompleteRulesetRefresh("singbox/geoip-cn", "https://up/geoip-cn.srs",
		time.Now(), []byte("\x00srs"), "application/vnd.srs", ""); err != nil {
		t.Fatal(err)
	}
	code, hdr, body = e.sub(subPath+"/ruleset/singbox/geoip-cn", "")
	if code != http.StatusOK || hdr.Get("Content-Type") != "application/vnd.srs" {
		t.Fatalf("srs 下载: %d %q", code, hdr.Get("Content-Type"))
	}
	if string(body) != "\x00srs" {
		t.Fatalf("srs 正文: %q", body)
	}
}

// 大件镜像按 Accept-Encoding 协商 gzip(ISS-18 后续):clash 的 direct.yaml 超过
// 2MB,不压缩在跨国链路上要传 30s+,撑爆客户端拉取超时。声明 gzip 的拿到
// Content-Encoding: gzip 且能解回原文;没声明的拿原文;.srs 这类压不动的回退
// 原文,不背一个假 Content-Encoding。
func TestRulesetFileGzipNegotiation(t *testing.T) {
	e, _ := newRulesetEnv(t, nil)
	_, subPath := e.createSubReadyUser("carol")

	// 种一个可压缩的大文件:重复的 yaml 行能压掉九成多。
	big := strings.Repeat("payload: [a.com, b.com, c.com, d.com, e.com]\n", rulesetGzipMinBytes/40)
	if _, err := e.st.CompleteRulesetRefresh("clash/direct", "https://up/direct.yaml",
		time.Now(), []byte(big), "text/yaml", ""); err != nil {
		t.Fatal(err)
	}

	// 不自动解压的客户端:Accept-Encoding 自己控制,才能验协商。
	rawClient := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	get := func(path, accept string) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, e.srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if accept != "" {
			req.Header.Set("Accept-Encoding", accept)
		}
		resp, err := rawClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp, data
	}

	// 声明 gzip:应返回压缩体且能解回原文。
	resp, data := get(subPath+"/ruleset/clash/direct", "gzip")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gzip 请求: %d", resp.StatusCode)
	}
	if resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("应回 gzip 编码: %v", resp.Header)
	}
	if resp.Header.Get("Vary") != "Accept-Encoding" {
		t.Fatalf("应带 Vary: %v", resp.Header)
	}
	if len(data) >= len(big) {
		t.Fatalf("压缩体不该更大: %d >= %d", len(data), len(big))
	}
	zr, err := gzip.NewReader(strings.NewReader(string(data)))
	if err != nil {
		t.Fatalf("gzip 流打不开: %v", err)
	}
	plain, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("解压: %v", err)
	}
	if string(plain) != big {
		t.Fatal("解回的正文与快照不一致")
	}

	// 不声明 gzip:拿原文。
	_, data = get(subPath+"/ruleset/clash/direct", "")
	if len(data) != len(big) {
		t.Fatalf("未声明 gzip 应回原文 %dB,得到 %dB", len(big), len(data))
	}
	resp2, _ := get(subPath+"/ruleset/clash/direct", "br")
	if resp2.Header.Get("Content-Encoding") == "gzip" {
		t.Fatal("没声明 gzip 不该回 gzip 编码")
	}
}

// GET /api/rulesets 只列「被启用规则引用的」组,并按格式聚合快照状态。
func TestListRulesetsAPI(t *testing.T) {
	e, _ := newRulesetEnv(t, nil)
	e.putRules([]model.RouteRule{
		{Type: model.RuleSetRef, Value: "media", Target: model.TargetReject, Enabled: true},
		{Type: model.RuleSetRef, Value: "lan", Target: model.TargetDirect, Enabled: false},
		{Type: model.RuleGeoIP, Value: "CN", Target: model.TargetDirect, Enabled: true},
	})
	if _, err := e.st.CompleteRulesetRefresh("clash/youtube", "u", time.Now(),
		[]byte("x"), "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.CompleteRulesetRefresh("singbox/geoip-cn", "u", time.Now(),
		nil, "", "拉取超时"); err != nil {
		t.Fatal(err)
	}

	code, data := e.api("GET", "/api/rulesets", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/rulesets: %d %s", code, data)
	}
	type fileView struct {
		Name      string `json:"name"`
		Ready     bool   `json:"ready"`
		LastError string `json:"last_error"`
	}
	type fmtView struct {
		Total int        `json:"total"`
		Ready int        `json:"ready"`
		Files []fileView `json:"files"`
	}
	var groups []struct {
		Key           string  `json:"key"`
		Name          string  `json:"name"`
		PrivateIP     bool    `json:"private_ip"`
		Clash         fmtView `json:"clash"`
		SingBox       fmtView `json:"sing_box"`
		Refreshing    bool    `json:"refreshing"`
		LastAttemptAt int64   `json:"last_attempt_at"`
		LastError     string  `json:"last_error"`
	}
	if err := json.Unmarshal(data, &groups); err != nil {
		t.Fatalf("解析响应: %v\n%s", err, data)
	}
	if len(groups) != 2 {
		t.Fatalf("应只有 media + geoip 两组(lan 停用): %s", data)
	}
	if groups[0].Key != "media" || groups[0].Name != "流媒体" {
		t.Fatalf("media 组应在目录序位置: %+v", groups[0])
	}
	if groups[0].Clash.Total != 8 || groups[0].Clash.Ready != 1 {
		t.Fatalf("media.clash 聚合不对: %+v", groups[0].Clash)
	}
	ytReady := false
	for _, f := range groups[0].Clash.Files {
		if f.Name == "youtube" {
			ytReady = f.Ready
		}
	}
	if !ytReady {
		t.Fatalf("youtube 文件应就绪: %+v", groups[0].Clash.Files)
	}
	if groups[1].Key != "geoip" || groups[1].Name != "GeoIP" {
		t.Fatalf("geoip 合成组应排最后: %+v", groups[1])
	}
	if groups[1].SingBox.Total != 1 || groups[1].SingBox.Ready != 0 {
		t.Fatalf("geoip 聚合不对: %+v", groups[1].SingBox)
	}
	if groups[1].LastError != "拉取超时" {
		t.Fatalf("组级 last_error 应带上文件错误: %+v", groups[1])
	}
}

// POST /api/rulesets/refresh 触发真实拉取并聚合到状态里。
func TestRefreshRulesetsAPI(t *testing.T) {
	var hits int64
	e, _ := newRulesetEnv(t, &hits)
	e.putRules([]model.RouteRule{
		{Type: model.RuleSetRef, Value: "telegram", Target: model.TargetDirect, Enabled: true},
	})

	code, data := e.api("POST", "/api/rulesets/refresh", map[string]any{"keys": []string{"telegram"}})
	if code != http.StatusAccepted {
		t.Fatalf("POST refresh: %d %s", code, data)
	}
	var resp struct {
		Started int `json:"started"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatal(err)
	}
	// telegram 组 = 2 clash + 2 singbox 文件。
	if resp.Started != 4 {
		t.Fatalf("started 应为 4: %s", data)
	}
	deadline := time.Now().Add(3 * time.Second)
	for atomic.LoadInt64(&hits) < 4 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if atomic.LoadInt64(&hits) != 4 {
		t.Fatalf("上游应被打 4 次,实际 %d", hits)
	}

	// 缺省 body 对全部引用组刷新;空表也要有 202。
	code, data = e.api("POST", "/api/rulesets/refresh", nil)
	if code != http.StatusAccepted {
		t.Fatalf("POST refresh 全量: %d %s", code, data)
	}

	// 没接拉取器的环境:接口本身不能 500,回 404。
	e2 := newEnv(t)
	if code, _ := e2.api("POST", "/api/rulesets/refresh", nil); code != http.StatusNotFound {
		t.Fatalf("未接拉取器的刷新应 404: %d", code)
	}
}

// ruleset_mode 枚举校验与保存语义。
func TestRulesetModeValidation(t *testing.T) {
	e := newEnv(t)

	code, data := e.api("PUT", "/api/settings", map[string]any{"ruleset_mode": "weird"})
	if code != http.StatusBadRequest || !strings.Contains(string(data), "ruleset_mode") {
		t.Fatalf("非法值应 400 带字段名: %d %s", code, data)
	}
	for _, v := range []string{"upstream", "privatized"} {
		if code, data := e.api("PUT", "/api/settings", map[string]any{"ruleset_mode": v}); code != http.StatusOK {
			t.Fatalf("%s 应 200: %d %s", v, code, data)
		}
		st, err := e.st.GetSettings()
		if err != nil || st.RulesetMode != v {
			t.Fatalf("%s 没落库: %q %v", v, st.RulesetMode, err)
		}
	}
	// 老客户端不带这个字段:零值保存即 upstream,不报 400。
	if code, data := e.api("PUT", "/api/settings", map[string]any{"sub_url_prefix": "https://x.example.com"}); code != http.StatusOK {
		t.Fatalf("缺省字段应兼容: %d %s", code, data)
	}
	st, _ := e.st.GetSettings()
	if st.RulesetMode != "" {
		t.Fatalf("零值应回空串: %q", st.RulesetMode)
	}
}

// 私有化模式的渲染:就绪文件换成镜像地址,未就绪仍走上游;upstream 模式逐字节不变。
func TestPrivatizedSubscriptionRendering(t *testing.T) {
	e, _ := newRulesetEnv(t, nil)
	user, subPath := e.createSubReadyUser("alice")
	e.putRules([]model.RouteRule{
		{Type: model.RuleSetRef, Value: "media", Target: model.TargetReject, Enabled: true},
	})
	if _, err := e.st.CompleteRulesetRefresh("clash/youtube", "https://up/youtube.mrs",
		time.Now(), []byte("mrs-bin"), "application/octet-stream", ""); err != nil {
		t.Fatal(err)
	}

	// upstream(默认):rule-provider 的 url 全是上游地址,不出现镜像 URL。
	// (provider 的本地缓存路径是 "./ruleset/<name>.mrs",断言要看 url 值,
	// 不能模糊搜 "/ruleset/" —— 那串本来就长在里面。)
	code, _, upstreamBody := e.sub(subPath+"/clash-meta", "")
	if code != http.StatusOK {
		t.Fatalf("取订阅: %d", code)
	}
	if strings.Contains(string(upstreamBody), "/ruleset/clash/") || strings.Contains(string(upstreamBody), "/ruleset/singbox/") {
		t.Fatal("upstream 模式不该出现镜像地址")
	}
	if !strings.Contains(string(upstreamBody), "jsdelivr.net") {
		t.Fatal("upstream 模式应保留上游地址")
	}

	// 切成私有化:youtube 已就绪 → 镜像地址;其余未就绪 → 仍上游。
	if code, data := e.api("PUT", "/api/settings", map[string]any{"ruleset_mode": "privatized"}); code != http.StatusOK {
		t.Fatalf("切私有化: %d %s", code, data)
	}
	code, _, privBody := e.sub(subPath+"/clash-meta", "")
	if code != http.StatusOK {
		t.Fatalf("私有化订阅: %d", code)
	}
	mirror := e.srv.URL + "/sub/" + user.SubToken + "/ruleset/clash/youtube"
	if !strings.Contains(string(privBody), mirror) {
		t.Fatalf("就绪文件应换成镜像地址 %q:\n%s", mirror, privBody)
	}
	if !strings.Contains(string(privBody), "cdn.jsdelivr.net") {
		t.Fatal("未就绪文件应保留上游地址")
	}
	// sing-box 未就绪仍是上游。
	code, _, sbBody := e.sub(subPath+"/sing-box", "")
	if code != http.StatusOK {
		t.Fatalf("sing-box 订阅: %d", code)
	}
	if strings.Contains(string(sbBody), "/ruleset/singbox/youtube") {
		t.Fatal("singbox/youtube 未就绪,不该换成镜像地址")
	}
}
