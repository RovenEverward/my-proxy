// Package updatecheck 问 GitHub 有没有比现在这份更新的发布。
//
// 它只做一件事:给出「我在跑的版本」和「上游最新的版本」,以及两者之间隔着几次
// 发布。它不下载、不替换、不重启 —— 升级永远是一次显式的人工决定。理由是更新
// 换掉的正是控制面自己:真换出一个起不来的二进制,连"查一下现在什么状态"这条路
// 都断了,只能登上机器救。所以这里只负责把消息送到人眼前。
//
// 查询结果在一段静默期内复用。GitHub 的匿名接口限流是按 IP 算的(每小时 60 次),
// 而版本号的更新速度是"周"级的 —— 每轮后台任务都去问一次,既没必要,也会在多个
// 控制面共用出口 IP 时把配额耗光。
package updatecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/buildinfo"
)

// Result 是一次更新检查的结论。
//
// Current 为空表示这个二进制没带版本号(从源码直接编出来、且没打 tag),
// 这时 Latest 只能取回来给人看,没法判断"是不是新的"。
type Result struct {
	Current string `json:"current"`
	Latest  string `json:"latest"`
	// Behind 是当前版本落后了几次发布;0 表示已是最新。
	Behind int `json:"behind"`
	// ReleaseURL 是最新发布在网页上的地址,给人点进去看改了什么。
	ReleaseURL string `json:"release_url"`
	// Notes 是发布的正文(markdown),截断过。
	Notes string `json:"notes"`
	// PublishedAt 是最新发布的发布时间。
	PublishedAt *time.Time `json:"published_at"`
	// CheckedAt 是这次结论是什么时候问出来的(命中缓存时是缓存的时间)。
	CheckedAt time.Time `json:"checked_at"`
	// Error 非空表示这次没问成,其余字段是上一次的结论。
	Error string `json:"error,omitempty"`
}

// Available 报告确实有一份更新的发布。
//
// 当前版本未知时一律返回 false:不知道自己在哪一版,就不能说别人更新。
// 这比猜一个要诚实,也避免了"从源码编出来的开发版天天提示有新版本"。
func (r Result) Available() bool { return r.Latest != "" && r.Current != "" && r.Behind > 0 }

// Checker 记住上次问到的结论,并按 interval 决定要不要再问一次。
type Checker struct {
	repo     string
	interval time.Duration
	now      func() time.Time

	// base 是 GitHub 接口的地址,测试里换成本地服务。
	base   string
	client *http.Client

	// version 是"我这一版"。默认取编译期信息,可注入 —— 测试要能拿各种版本号
	// (含伪版本、空版本)去跑判据,而不是只能测碰巧编出来的那一版。
	version func() string

	last Result
}

// New 造一个检查器。repo 是 owner/name,interval 是两次真实请求之间最短的间隔。
func New(repo string, interval time.Duration) *Checker {
	if interval <= 0 {
		interval = DefaultInterval
	}
	return &Checker{
		repo:     repo,
		interval: interval,
		now:      time.Now,
		base:     "https://api.github.com",
		client:   &http.Client{Timeout: 15 * time.Second},
		version:  buildinfo.Version,
	}
}

// DefaultInterval 是默认静默期。
//
// 6 小时的理由:版本的更新速度是"周"级,而 hourly 的配额是 60 次 —— 一天问 4 次,
// 即使好几台控制面共用一个出口 IP 也远远用不完。同时它足够及时:一天之内总会
// 至少问到一次,不会出现"发布三天了界面上还没动静"。
const DefaultInterval = 6 * time.Hour

// Check 给出结论。距离上次成功查询不足间隔时直接复用,不发请求。
//
// force 为真时忽略间隔(给人手动触发的入口用:他刚发了新版本想立刻看到)。
func (c *Checker) Check(ctx context.Context, force bool) Result {
	c.last.Current = c.version()
	if !force && !c.last.CheckedAt.IsZero() && c.now().Sub(c.last.CheckedAt) < c.interval {
		return c.last
	}

	latest, url, notes, published, err := c.fetchLatest(ctx)
	if err != nil {
		// 保留上一次的结论,只把错误记上 —— 断网不该把已经知道的信息一起抹掉。
		c.last.Error = err.Error()
		return c.last
	}

	c.last.Latest = latest
	c.last.ReleaseURL = url
	c.last.Notes = notes
	c.last.PublishedAt = published
	c.last.CheckedAt = c.now()
	c.last.Error = ""
	c.last.Behind = c.countBehind(ctx, latest)
	return c.last
}

// Snapshot 返回上一次的结论,不触发任何请求。
func (c *Checker) Snapshot() Result {
	r := c.last
	r.Current = c.version()
	return r
}

// fetchLatest 问最新发布是哪一版。
func (c *Checker) fetchLatest(ctx context.Context) (version, url, notes string, published *time.Time, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/repos/"+c.repo+"/releases/latest", nil)
	if err != nil {
		return "", "", "", nil, err
	}
	// GitHub 的接口要求带 User-Agent,不带会直接 403。
	req.Header.Set("User-Agent", "mpx")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("请求 GitHub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", "", nil, fmt.Errorf("请求 GitHub: HTTP %d", resp.StatusCode)
	}

	var body struct {
		TagName     string `json:"tag_name"`
		HTMLURL     string `json:"html_url"`
		Body        string `json:"body"`
		PublishedAt string `json:"published_at"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", "", "", nil, fmt.Errorf("读 GitHub 的发布信息: %w", err)
	}
	if body.TagName == "" {
		return "", "", "", nil, fmt.Errorf("GitHub 没有给出最新版的版本号")
	}
	if body.PublishedAt != "" {
		if t, perr := time.Parse(time.RFC3339, body.PublishedAt); perr == nil {
			published = &t
		}
	}
	return body.TagName, body.HTMLURL, truncateNotes(body.Body), published, nil
}

// maxNotes 是发布正文保留的字数(按 rune,不是字节)。
//
// 完整正文可能几十 KB(发布说明里常贴整段用法),而它每 6 小时就要跟着 status
// 走一遍接口。头部几百字足够回答"这次改了什么"。
const maxNotes = 2000

func truncateNotes(s string) string {
	s = strings.TrimSpace(s)
	// maxNotes 按 rune 计:UTF-8 多字节文字的字节数会大于字数,用 len(s) 判断
	// 再用 [:maxNotes] 切 rune 切片会 panic(v0.5.0 起服后查更新就踩过)。
	r := []rune(s)
	if len(r) <= maxNotes {
		return s
	}
	return strings.TrimSpace(string(r[:maxNotes])) + "\n…(截断,完整说明见发布页)"
}

// countBehind 数当前版本与 latest 之间隔了几次发布。
//
// 只有确认 latest 确实比当前版本新时才数;数不出来或者比不出来都返回 0,因为
// 0 的含义是"没有更新" —— 让 Available 跟 Ahead 用同一条判据,免得出现"比大小
// 说不新、数数说有更新"这种自相矛盾的结论。
//
// 这一步看着有点浪费(前面 Ahead 已经比过一次),但它换来的是**只有真落后时**
// 才多打一次接口:多数轮次里当前版本已经是最新,Ahead 当场否掉,不会为一个已经
// 知道的答案再跑一趟 GitHub。
func (c *Checker) countBehind(ctx context.Context, latest string) int {
	cur := c.version()
	if cur == "" {
		return 0
	} else if cur == latest {
		return 0
	}
	// 排在后面的版本不需要数:比不过就不是"落后",数出来只会添乱。
	if !Ahead(cur, latest) {
		return 0
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/repos/"+c.repo+"/releases?per_page=100", nil)
	if err != nil {
		return 1
	}
	req.Header.Set("User-Agent", "mpx")
	resp, err := c.client.Do(req)
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}

	var releases []struct {
		TagName    string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&releases); err != nil {
		return 1
	}

	n := 0
	for _, rel := range releases {
		if rel.Draft || rel.Prerelease {
			continue
		}
		if rel.TagName == cur {
			return n
		}
		n++
	}
	// 列表里没有当前版本:多半是列表被截断了(超过 100 个发布),或者当前版本
	// 比列表里的都新。两种都只能说"至少落后不知道多少次",保守地报 1。
	return 1
}

// Ahead 报告 latest 是否比 current 新。
//
// 只比数字段。两边的版本号都形如 vMAJOR.MINOR.PATCH,比不出来(带后缀、格式怪)
// 时返回 false —— 宁可不说,也不要因为一个不认识的版本号天天报"有新版本"。
func Ahead(current, latest string) bool {
	cur, ok1 := parseVersion(current)
	lat, ok2 := parseVersion(latest)
	if !ok1 || !ok2 {
		return false
	}
	for i := 0; i < 3; i++ {
		if lat[i] != cur[i] {
			return lat[i] > cur[i]
		}
	}
	return false
}

// SameMajorMinor 报告两个版本号的主版本与次版本是否一致。
//
// 它用来回答"CLI 和控制面是不是同一代":API 只新增不修改,同一主/次版本之间
// 一般可以直接对话。两边任一边为空、或者带后缀(rc、dirty、伪版本)时返回 true,
// 也就是"不判定为不一致" —— 比不出来时宁可不警告,也不要凭一个不认识的版本号
// 吓人。
func SameMajorMinor(a, b string) bool {
	va, ok1 := parseVersion(a)
	vb, ok2 := parseVersion(b)
	if !ok1 || !ok2 {
		return true
	}
	return va[0] == vb[0] && va[1] == vb[1]
}

func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	// 带后缀的(比如 v0.2.0-rc1、v0.2.0-dirty)不比大小。
	if v == "" || strings.ContainsAny(v, "-+") {
		return out, false
	}
	parts := strings.Split(v, ".")
	if len(parts) > 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
