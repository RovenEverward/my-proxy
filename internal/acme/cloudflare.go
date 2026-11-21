package acme

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// DNSProvider 是签发流程对 DNS 服务的全部要求:给一个域名摆出一条
// _acme-challenge TXT,签完了再收掉。实现是 Cloudflare,但流程只依赖这个
// 接口 —— 测试用一个内存假实现,不碰网络。
type DNSProvider interface {
	// SetTXT 在 _acme-challenge.<domain> 上摆出 value 这条 TXT 记录;
	// 返回一个撤销句柄,签完(无论成败)必须调用清理。
	SetTXT(ctx context.Context, domain, value string) (cleanup func(context.Context) error, err error)
}

// Cloudflare 是走 Cloudflare API v4 的 DNSProvider。
//
// token 只进 Authorization 头,不进 URL、不进日志;HTTP 调用带总超时 —
// CF 侧卡死时签发任务该失败,不该吊死。
type Cloudflare struct {
	Token string
	// APIBase 是 API 根;默认 https://api.cloudflare.com/client/v4,
	// 测试里换成 httptest。
	APIBase string
	HTTP    *http.Client
}

const cloudflareAPIBase = "https://api.cloudflare.com/client/v4"

// cfClient 是带超时的 HTTP 客户端(整个 ACME 流程共享一份也行)。
func (cf *Cloudflare) client() *http.Client {
	if cf.HTTP != nil {
		return cf.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// cfError 是 CF 回的非 2xx。errors[] 里第一条 message 最有用。
type cfError struct {
	Status int
	Msg    string
}

func (e *cfError) Error() string {
	return fmt.Sprintf("cloudflare API %d: %s", e.Status, e.Msg)
}

// cfEnvelope 是 CF 响应的统一信封:成功时 result 是要的,失败时 errors 是要的。
type cfEnvelope struct {
	Success bool            `json:"success"`
	Errors  []cfErrItem     `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

type cfErrItem struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// do 发一条 CF API 请求并把 JSON 响应解进 out(可以是 *X 或 *[]X 形状的 result)。
func (cf *Cloudflare) do(ctx context.Context, method, path string, body any, out any) error {
	base := cf.APIBase
	if base == "" {
		base = cloudflareAPIBase
	}
	var rd *strings.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("序列化 cloudflare 请求体: %w", err)
		}
		rd = strings.NewReader(string(raw))
	} else {
		rd = strings.NewReader("")
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cf.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := cf.client().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	var env cfEnvelope
	if err := json.NewDecoder(res.Body).Decode(&env); err != nil {
		return fmt.Errorf("cloudflare 响应不是合法 JSON: %w", err)
	}
	if !env.Success || res.StatusCode < 200 || res.StatusCode >= 300 {
		msg := http.StatusText(res.StatusCode)
		if len(env.Errors) > 0 && env.Errors[0].Message != "" {
			msg = env.Errors[0].Message
		}
		return &cfError{Status: res.StatusCode, Msg: msg}
	}
	if out != nil && len(env.Result) > 0 {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("解析 cloudflare result: %w", err)
		}
	}
	return nil
}

// zone 是 zones 查询结果里我们关心的两个字段。
type cfZone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// dnsRecord 是 dns_records 的一条记录。
type cfDNSRecord struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
}

// zoneID 找出覆盖 domain 的 zone:逐级剥掉最左标签问 zones?name=。
// 例如 cp.example.net 先试整名、再试 example.net、再试 net —— CF 是
// 域名实际托管在哪儿,zone 就在哪一级。
func (cf *Cloudflare) zoneID(ctx context.Context, domain string) (string, error) {
	d := domain
	for {
		var zones []cfZone
		if err := cf.do(ctx, http.MethodGet, "/zones?name="+d, nil, &zones); err != nil {
			return "", err
		}
		if len(zones) > 0 {
			return zones[0].ID, nil
		}
		i := strings.IndexByte(d, '.')
		if i < 0 {
			return "", fmt.Errorf("cloudflare 找不到覆盖 %s 的 zone:这个域名没托管在这个账号下", domain)
		}
		d = d[i+1:]
	}
}

// txtRecord 描述一条 _acme-challenge TXT。
type cfTXTRef struct {
	ZoneID   string
	RecordID string
}

// listTXT 列出某 FQDN 下所有 TXT 记录。
func (cf *Cloudflare) listTXT(ctx context.Context, zoneID, fqdn string) ([]cfDNSRecord, error) {
	var recs []cfDNSRecord
	if err := cf.do(ctx, http.MethodGet,
		fmt.Sprintf("/zones/%s/dns_records?type=TXT&name=%s", zoneID, fqdn), nil, &recs); err != nil {
		return nil, err
	}
	return recs, nil
}

// deleteRecord 删一条 DNS 记录;已经没了(404)不算错。
func (cf *Cloudflare) deleteRecord(ctx context.Context, zoneID, recordID string) error {
	err := cf.do(ctx, http.MethodDelete,
		fmt.Sprintf("/zones/%s/dns_records/%s", zoneID, recordID), nil, nil)
	var ce *cfError
	if errors.As(err, &ce) && ce.Status == http.StatusNotFound {
		return nil
	}
	return err
}

// SetTXT 摆出 _acme-challenge.<domain> 的 TXT。
//
// 先把同名旧记录清掉再建:上次签发的 cleanup 若没跑成(进程被杀、CF 侧错误),
// 残留 TXT 会让 CA 读到两条记录、其中一条是错的,挑战必挂 —— 同名记录对我们
// 只有一个含义(这次的 token),清掉再建永远是对的。
//
// 返回的 cleanup 删掉这次建的那一条;没建成时 cleanup 是 nil。
func (cf *Cloudflare) SetTXT(ctx context.Context, domain, value string) (func(context.Context) error, error) {
	if cf.Token == "" {
		return nil, errors.New("没有配置 Cloudflare API token")
	}
	fqdn := "_acme-challenge." + domain
	zid, err := cf.zoneID(ctx, domain)
	if err != nil {
		return nil, err
	}
	old, err := cf.listTXT(ctx, zid, fqdn)
	if err != nil {
		return nil, err
	}
	for _, r := range old {
		if err := cf.deleteRecord(ctx, zid, r.ID); err != nil {
			return nil, fmt.Errorf("清残留的 %s 记录: %w", fqdn, err)
		}
	}
	var rec cfDNSRecord
	if err := cf.do(ctx, http.MethodPost, fmt.Sprintf("/zones/%s/dns_records", zid), map[string]any{
		"type":    "TXT",
		"name":    fqdn,
		"content": value,
		"ttl":     60, // 验证完就删,TTL 只是兜底:短一点免得缓存里留尸体
	}, &rec); err != nil {
		return nil, err
	}
	return func(ctx context.Context) error {
		return cf.deleteRecord(ctx, zid, rec.ID)
	}, nil
}
