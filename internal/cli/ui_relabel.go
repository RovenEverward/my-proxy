package cli

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/ui"
)

// relabelRunner 把页面的批量识别接到 CLI 那份 relabelPlan / relabelApply 上:
// 计算与写入只有一份实现,页面看到什么、CLI 写入什么必然一致。
type relabelRunner struct{}

// uiClient 用一个临时的 Client 打这一次请求:地址/凭据/传输都是页面传进来的
// 当前值(切换控制面后跟着变),不影响 CLI 自己的配置解析。
func uiClient(up *url.URL, token string, tr http.RoundTripper) *Client {
	return &Client{
		server: strings.TrimSuffix(up.String(), "/"),
		token:  token,
		http:   &http.Client{Transport: tr},
	}
}

func (relabelRunner) Plan(ctx context.Context, up *url.URL, token string, tr http.RoundTripper, req ui.RelabelRequest) ([]ui.RelabelRow, []ui.RelabelPlanItem, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	c := uiClient(up, token, tr)
	views, err := listNodes(c)
	if err != nil {
		return nil, nil, err
	}
	targets := views
	if !req.All {
		want := map[int64]bool{}
		for _, id := range req.IDs {
			want[id] = true
		}
		targets = make([]model.NodeView, 0, len(req.IDs))
		for _, v := range views {
			if want[v.ID] {
				targets = append(targets, v)
			}
		}
	}
	rows, plans, err := relabelPlan(c, views, targets)
	if err != nil {
		return nil, nil, err
	}
	outRows := make([]ui.RelabelRow, 0, len(rows))
	for _, r := range rows {
		outRows = append(outRows, ui.RelabelRow{ID: r.ID, Name: r.Name, Old: r.Old, New: r.New, Note: r.Note})
	}
	outPlans := make([]ui.RelabelPlanItem, 0, len(plans))
	for _, p := range plans {
		outPlans = append(outPlans, ui.RelabelPlanItem{ID: p.ID, Name: p.Name, Metadata: p.Metadata})
	}
	return outRows, outPlans, nil
}

func (relabelRunner) Apply(ctx context.Context, up *url.URL, token string, tr http.RoundTripper, plans []ui.RelabelPlanItem) ([]ui.RelabelResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	in := make([]relabelPlanItem, 0, len(plans))
	for _, p := range plans {
		in = append(in, relabelPlanItem{ID: p.ID, Name: p.Name, Metadata: p.Metadata})
	}
	results, err := relabelApply(uiClient(up, token, tr), in)
	if err != nil {
		return nil, err
	}
	out := make([]ui.RelabelResult, 0, len(results))
	for _, r := range results {
		out = append(out, ui.RelabelResult{ID: r.ID, Name: r.Name, Display: r.Display, Skipped: r.Skipped, Reason: r.Reason})
	}
	return out, nil
}
