package cdn

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

// MeasureSpeed 对单个候选 IP 做限时下载测速(UC-31,方法对齐
// CloudflareSpeedTest):DialContext 绑候选 IP 直拨,URL 域名照常做
// SNI/Host —— TLS 身份是域名,IP 只是落点;请求带浏览器 UA(公共测速
// 端点与 CF 边缘对脚本 UA 会拦)。返回速度与响应头里带的机房码(有
// CF-RAY 再刷新一次,测速路径可能落别的机房)。
// 计时从首个字节起算,排除建连与 TLS 抖动;限时内没下完也按实收字节/
// 实耗时间折算,所以测速地址文件「够大」不是硬性要求,但太小(秒级
// 下完)会让快 IP 测不准 —— 地址端建议给几十 MB 量级。
func MeasureSpeed(ctx context.Context, ip, rawURL string, timeout time.Duration) (mbps float64, colo string, err error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return 0, "", fmt.Errorf("测速地址必须是 https URL: %s", rawURL)
	}
	return measureSpeed(ctx, ip, "443", u, timeout)
}

// measureSpeed 是 MeasureSpeed 的可测试内核:port 提出来是因为单测要
// 对着 httptest 的随机端口打。
func measureSpeed(ctx context.Context, ip, port string, u *url.URL, timeout time.Duration) (float64, string, error) {
	dialAddr := net.JoinHostPort(ip, port)
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: timeout}).DialContext(ctx, network, dialAddr)
		},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // 身份是域名;IP 只是落点,不校验证书
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, "GET", u.String(), nil)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36")
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("测速地址回了 %d", resp.StatusCode)
	}
	var colo string
	if edge, c, err := parseCFRay("CF-RAY:" + resp.Header.Get("CF-RAY") + "\n"); err == nil && edge {
		colo = c
	}
	// 限时 io.Copy:reqCtx 到点掐断连接,超时以「已收到的字节/实耗
	// 时间」折算 —— 读空/中途出错都按实收算,不算失败。
	var received int64
	start := time.Now()
	buf := make([]byte, 32*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		received += int64(n)
		if rerr != nil {
			break
		}
	}
	elapsed := time.Since(start).Seconds()
	if elapsed <= 0 || received <= 0 {
		return 0, colo, fmt.Errorf("测速没有收到数据")
	}
	// bytes/s -> MB/s(MiB/s 口径,对齐 CFST 输出习惯)。
	return float64(received) / elapsed / (1024 * 1024), colo, nil
}
