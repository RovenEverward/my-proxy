package cli

import (
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// 表格里显示的是生效值,不是填进去的值;没得显示的项一律是 "-",不是空白 ——
// 空白会让人以为这列坏了,而 "-" 至少说明这里本来就没有。
func TestHostRowShowsEffectiveValues(t *testing.T) {
	v := model.HostView{
		Host:      model.Host{ID: 7, InboundID: 2, Remark: "香港 WS"},
		Effective: model.Exposure{Address: "203.0.113.46", Port: 30443, Path: "/hk"},
	}

	got := hostRow(v)
	want := []string{"7", "2", "香港 WS", "203.0.113.46", "30443", "-", "-", "/hk", "-"}

	if len(hostHeaders) != len(got) {
		t.Fatalf("表头 %d 列,数据 %d 列", len(hostHeaders), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 列(%s): got %q want %q", i+1, hostHeaders[i], got[i], want[i])
		}
	}
}
