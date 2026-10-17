package model

import (
	"encoding/json"
	"testing"
)

func TestLocationMetadataAndValidation(t *testing.T) {
	n := LocationMetadata{Region: "US", City: "洛杉矶", Letter: "A"}
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*LocationMetadata){
		func(m *LocationMetadata) { m.Region = string(make([]byte, 81)) },
		func(m *LocationMetadata) { m.City = "bad\tcity" },
		func(m *LocationMetadata) { m.Letter = "lower" },
	} {
		bad := n
		mutate(&bad)
		if err := bad.Validate(); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	// 老客户端写进来的 line / purposes 解析时直接丢掉,不报错也不影响校验。
	legacy := []byte(`{"region":"US","city":"洛杉矶","line":"cn2","purposes":["media"]}`)
	var m LocationMetadata
	if err := json.Unmarshal(legacy, &m); err != nil {
		t.Fatalf("老字段应被忽略: %v", err)
	}
	if m.Region != "US" || m.City != "洛杉矶" {
		t.Fatalf("解析结果 %+v", m)
	}
}
