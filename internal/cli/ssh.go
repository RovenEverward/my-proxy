package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// UC-29:本机 CLI 走 SSH 通道 —— SSH 就是认证,不存任何控制面凭据。
//
// 一次 API 调用 = 一条 ssh 命令:把 HTTP 请求序列化后喂给远端
// `mpx api-relay`,远端用 /etc/mpx/env 的主钥匙打到 127.0.0.1 的控制面,
// 响应原样回吐。token 永不离开服务器,本机 profile 里没有凭据可过期、
// 可泄露、可续期 —— 能 ssh 到服务器就是全部认证。
//
// 与 UC-28(SSH 铸会话)的区别:那里 SSH 是「发证的引子」,凭据落本机后仍要
// 过期续期;这里 SSH 是通道本身,凭据这个概念在 CLI 侧不存在。
//
// 只调系统的 ssh 命令:~/.ssh/config 的别名、ProxyJump、agent、known_hosts
// 都由它处理,行为与使用者手敲完全一致,不引第三方 SSH 库。

// sshConnectTimeout 是一次 ssh 请求的上限:连不上多半是地址或密钥不对,
// 等久了也没有意义。慢命令(备份下载)有自己的 ctx deadline,会盖过它 ——
// 那里以调用方的预算为准。
const sshConnectTimeout = 60 * time.Second

// relayPlaceholder 是发给 relay 的 HTTP 请求里用的占位 host:真正的目标是
// 远端的 127.0.0.1:<apiPort>,写死在远端,本机不拼。
const relayPlaceholder = "mpx-relay"

// sshRelayCmd 是 profile 里没存自定义命令时用的远端通道命令。
//
// mpx 按 command -v 找,PATH 里没有时退回 /usr/local/bin/mpx —— 那是
// deploy/install.sh 的固定落点。端口取控制面 API 的端口(serve --addr 的
// 那个),relay 用它环回直连。
func sshRelayCmd(apiPort int) string {
	return `MPX="$(command -v mpx || echo /usr/local/bin/mpx)" && "$MPX" api-relay --port ` +
		strconv.Itoa(apiPort)
}

// sshArgs 拼一次通道调用的 ssh 参数。target 是 ssh 的目标串:user@host、
// ~/.ssh/config 的别名,或连人带参数的一串(如 "root@host -p 5522")——
// 它按空白切开后接在 ssh 选项后面。
func sshArgs(target, remoteCmd string) []string {
	args := []string{
		"-T",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
		"-o", "StrictHostKeyChecking=accept-new",
	}
	args = append(args, strings.Fields(target)...)
	return append(args, remoteCmd)
}

// sshRequest 经 ssh 通道发一条 HTTP 请求,返回尚未读取正文的响应。
//
// 请求被序列化成标准 HTTP/1.1 报文喂给远端 relay;占位 host 只是满足
// HTTP 报文格式,relay 端会换成 127.0.0.1:<port>。调用方负责 Close 响应体。
func sshRequest(ctx context.Context, target, remoteCmd string, req *http.Request) (*http.Response, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, errors.New("SSH 目标为空")
	}
	if remoteCmd == "" {
		return nil, errors.New("远端 relay 命令为空")
	}

	var body bytes.Buffer
	if err := req.WriteProxy(&body); err != nil {
		return nil, fmt.Errorf("序列化请求: %w", err)
	}

	cmd := exec.CommandContext(ctx, "ssh", sshArgs(target, remoteCmd)...)
	cmd.Stdin = &body
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("ssh %s 超时或取消: %w", target, ctxErr)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		if strings.Contains(msg, "unknown command") || strings.Contains(msg, "api-relay") {
			return nil, fmt.Errorf("ssh %s: %s(控制面 mpx 版本过旧,先在服务器上升级到含 api-relay 的版本)", target, msg)
		}
		return nil, fmt.Errorf("ssh %s: %s", target, msg)
	}
	resp, err := http.ReadResponse(bufio.NewReader(&stdout), req)
	if err != nil {
		head := stdout.String()
		if len(head) > 200 {
			head = head[:200]
		}
		return nil, fmt.Errorf("ssh %s: relay 回的报文解析不了(%v;开头: %q)", target, err, head)
	}
	return resp, nil
}

// newRelayRequest 造一条走 relay 的请求:URL 只带 path/query,占位 host。
// 序列化时走绝对式(WriteProxy),relay 按它重组目标。
func newRelayRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	u := &url.URL{Scheme: "http", Host: relayPlaceholder}
	if p, err := url.Parse(path); err == nil {
		u.Path, u.RawQuery = p.Path, p.RawQuery
	} else {
		u.Path = path
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	return req, nil
}

// sshRoundTrip 是 doOnce 的 SSH 通道版本,签名与返回语义对齐(含 429 的
// retryAfter),让 doRetry 对传输方式无感。
func (c *Client) sshRoundTrip(ctx context.Context, method, path string, payload []byte, hasBody bool) ([]byte, time.Duration, error) {
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	req, err := newRelayRequest(ctx, method, path, reader)
	if err != nil {
		return nil, 0, err
	}
	if hasBody {
		req.Header.Set("Content-Type", "application/json")
	}
	remote := c.sshCmd
	if remote == "" {
		remote = sshRelayCmd(apiPortFromServer(c.server))
	}
	resp, err := sshRequest(ctx, c.sshTarget, remote, req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("读取响应: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		wait := time.Duration(0)
		if resp.StatusCode == http.StatusTooManyRequests {
			if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
				wait = time.Duration(s) * time.Second
			}
		}
		return nil, wait, &APIError{StatusCode: resp.StatusCode, Message: errorMessage(resp.StatusCode, data), UIHint: errorUIHint(data), Body: data}
	}
	return data, 0, nil
}

// sshDownload 是 download 的 SSH 通道版本:响应正文流式写进 w。
func (c *Client) sshDownload(ctx context.Context, path string, w io.Writer) error {
	req, err := newRelayRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	remote := c.sshCmd
	if remote == "" {
		remote = sshRelayCmd(apiPortFromServer(c.server))
	}
	resp, err := sshRequest(ctx, c.sshTarget, remote, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return &APIError{StatusCode: resp.StatusCode, Message: errorMessage(resp.StatusCode, data), UIHint: errorUIHint(data)}
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		return fmt.Errorf("写入备份文件: %w", err)
	}
	return nil
}

// sshTargetFromServer 在 --ssh 没带值时用:拿 connect 地址的主机名当 ssh 目标。
// https:// 前缀与端口都剥掉;IPv6 剥方括号。host 是 IP 字面量时一样可行 —
// ssh root@IP 与 ssh 别名对系统 ssh 来说没有差别。
func sshTargetFromServer(server string) string {
	s := strings.TrimSuffix(strings.TrimPrefix(server, "https://"), "/")
	host, _, err := net.SplitHostPort(s)
	if err == nil {
		return strings.Trim(host, "[]")
	}
	return s
}

// apiPortFromServer 取控制面地址里的端口,没写就用 8080(与 connect 一致)。
func apiPortFromServer(server string) int {
	s := strings.TrimSuffix(strings.TrimPrefix(server, "https://"), "/")
	_, port, err := net.SplitHostPort(s)
	if err != nil {
		return defaultControlPort
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return defaultControlPort
	}
	return p
}
