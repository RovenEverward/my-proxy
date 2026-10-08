// Package cli 实现 mpx 的命令行客户端。
//
// 它只通过控制面的 /api 说话:自己不开数据库、不渲染节点配置,判断逻辑留在
// 服务端那一份实现里,CLI 只负责取参数、发请求、把响应摆成人能读的样子。
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"
)

// Client 是控制面管理接口的客户端。
type Client struct {
	server string
	token  string
	http   *http.Client
}

// APIError 是控制面返回的非 2xx 响应。
//
// 单独成一个类型,是为了让调用方能按状态码分支(404 与 401 的处理不同);
// Error() 直接给出服务端那句中文说明,不再包一层"请求失败"把原因盖掉。
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string { return e.Message }

// newClient 按 命令行 flag > 环境变量 > 配置文件 的优先级组装一个客户端。
func newClient() (*Client, error) {
	server, token, err := resolve()
	if err != nil {
		return nil, err
	}
	if server == "" {
		return nil, errors.New("没配置控制面地址:先跑 mpx login --server URL --token TOKEN,或设置 MPX_SERVER")
	}
	return &Client{
		server: strings.TrimSuffix(server, "/"),
		token:  token,
		// 超时按次给(见 doWithin),不在这里定一个套在所有接口上的值:读一条记录
		// 和往节点上装核心的合理上限差三个数量级。
		http: &http.Client{},
	}, nil
}

const (
	// defaultTimeout 是管理接口的常规上限。控制面一般就在本机(或一条 ssh 隧道
	// 的另一头),正常几毫秒就回。
	defaultTimeout = 30 * time.Second
	// installTimeout 是安装节点核心的上限:控制面要下载一个几十兆的发布包,再推
	// 同样大小的东西过 ssh 到节点上,慢链路下几分钟很正常。
	installTimeout = 15 * time.Minute
	// doctorTimeout 是自检的上限。自检要对每台节点各查一次(每次最多 checkBudget
	// 30 秒),十几台节点串起来就是几分钟 —— 它本来就该慢慢跑完,中途被客户端
	// 掐掉只会留下一半的表。
	doctorTimeout = 10 * time.Minute
	// nodeStatusTimeout 是现场查节点状态的上限。它必须比控制面自己的
	// checkBudget(30 秒)长:真正在等的是控制面那条 ssh,如果这边先断,
	// 控制面还在跑、结果也落了库,而使用者只看到一个超时 —— 他会以为没查成,
	// 然后再查一次。留一倍余量,让控制面有机会说话。
	nodeStatusTimeout = time.Minute
	// backupTimeout 是下载备份的上限。备份里主要是库快照,可能随流量采样增长;
	// 给得比普通接口宽,但不必像装核心那么长。
	backupTimeout = 5 * time.Minute
)

// rawBody 是跳过 JSON 序列化、原样发出的请求体。模板正文就是模板本身
// (YAML / JSON 文本),不能包一层 JSON 引号再发。
type rawBody []byte

// do 以常规上限发一次请求。
func (c *Client) do(method, path string, body any) ([]byte, error) {
	return c.doWithin(defaultTimeout, method, path, body)
}

// doWithin 以指定的上限发一次请求。
//
// 原始字节要一路留到输出层:json 模式直接重排它,表格模式才按需解出字段。
// 这样客户端不必复刻一套服务端的结构体,也就不会有字段漂移。
func (c *Client) doWithin(timeout time.Duration, method, path string, body any) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		switch b := body.(type) {
		case rawBody:
			reader = bytes.NewReader(b)
		default:
			payload, err := json.Marshal(body)
			if err != nil {
				return nil, fmt.Errorf("序列化请求体: %w", err)
			}
			reader = bytes.NewReader(payload)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, c.server+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连接控制面 %s 失败: %w", c.server, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{StatusCode: resp.StatusCode, Message: errorMessage(resp.StatusCode, data)}
	}
	return data, nil
}

// download 以 GET 发一次请求,把响应正文流式写进 w。
//
// 备份接口返回的是 tar.gz 字节流,不是 JSON:do 那条路要 io.ReadAll 全量进内存
// 再返回 []byte,对可能长大的备份不合适,而且错误信息也拿不到。非 2xx 时照常
// 读回正文翻成 APIError。
func (c *Client) download(path string, w io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), backupTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.server+path, nil)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("连接控制面 %s 失败: %w", c.server, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return &APIError{StatusCode: resp.StatusCode, Message: errorMessage(resp.StatusCode, data)}
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		return fmt.Errorf("写入备份文件: %w", err)
	}
	return nil
}

// errorMessage 取出控制面 {"error": "..."} 里的那句话。
// 拿不到合法 JSON 时退回状态码加原文:宁可把一整页 HTML 错误摆出来,也不要
// 只留一句"status 500"把真正的原因丢掉。
func errorMessage(code int, data []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &e) == nil && e.Error != "" {
		return e.Error
	}
	if text := strings.TrimSpace(string(data)); text != "" {
		return fmt.Sprintf("HTTP %d: %s", code, text)
	}
	return fmt.Sprintf("HTTP %d", code)
}

// emitJSON 把服务端的原始响应重新缩进后输出。
//
// 不重新解码再编码:字段集由服务端定义,客户端复刻一份结构体会随服务端漂移。
func emitJSON(raw []byte) error {
	// 服务端用 json.Encoder 输出,正文自带换行;不先剪掉就会多打一个空行。
	raw = bytes.TrimRight(raw, " \t\r\n")
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		// 不是 JSON 就原样透传,别把响应内容吃掉。
		buf.Reset()
		buf.Write(raw)
	}
	buf.WriteByte('\n')
	_, err := os.Stdout.Write(buf.Bytes())
	return err
}

// emitJSONValue 把客户端自己拼出来的结构按同样的缩进规则打出去。
//
// 与 emitJSON 分开:那个用于透传服务端响应,这个用于批量/导入这种客户端自己
// 攒出来的报告,形状由客户端定义。
func emitJSONValue(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(append(b, '\n'))
	return err
}

// report 输出一次写操作的结果:json 模式给服务端原始响应,表格模式给人话。
func report(raw []byte, format string, a ...any) error {
	if opts.output == outputJSON {
		return emitJSON(raw)
	}
	_, err := fmt.Fprintf(os.Stdout, format+"\n", a...)
	return err
}

// decode 把原始响应解成表格模式需要的那几个字段。
func decode[T any](raw []byte) (T, error) {
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, fmt.Errorf("解析控制面响应: %w", err)
	}
	return v, nil
}

// tableText 把一张带表头的表渲染成文本;没有数据时明确写出"(空)"而不是什么都不打。
//
// 渲染与打印分开,是为了批量命令能把每个用户的结果攒成一个字符串再决定怎么输出
// (逐个打、还是包进 JSON),不至于一边打一边拼。
func tableText(headers []string, rows [][]string) string {
	if len(rows) == 0 {
		return "(空)\n"
	}
	var buf bytes.Buffer
	w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, strings.Join(headers, "\t"))
	for _, row := range rows {
		fmt.Fprintln(w, strings.Join(row, "\t"))
	}
	_ = w.Flush()
	return buf.String()
}

// writeTable 把表格打到标准输出。
func writeTable(headers []string, rows [][]string) error {
	_, err := os.Stdout.WriteString(tableText(headers, rows))
	return err
}

// writeKV 打一张两列的详情表,用来显示一条记录的全部字段。
func writeKV(pairs [][2]string) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "字段\t值")
	for _, p := range pairs {
		fmt.Fprintf(w, "%s\t%s\n", p[0], dash(p[1]))
	}
	return w.Flush()
}

// dash 把空值显示成 "-",免得表格里出现看不出来的空白。
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
