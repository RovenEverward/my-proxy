package model

import (
	"encoding/json"
	"testing"
)

func TestPurposeMetadataAndValidation(t *testing.T) {
	n := PurposeMetadata{Region: "US", City: "洛杉矶", Letter: "A"}
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*PurposeMetadata){
		func(m *PurposeMetadata) { m.Region = string(make([]byte, 81)) },
		func(m *PurposeMetadata) { m.City = "bad\tcity" },
		func(m *PurposeMetadata) { m.Letter = "lower" },
	} {
		bad := n
		mutate(&bad)
		if err := bad.Validate(); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	// 老客户端写进来的 line / purposes 解析时直接丢掉,不报错也不影响校验。
	legacy := []byte(`{"region":"US","city":"洛杉矶","line":"cn2","purposes":["media"]}`)
	var m PurposeMetadata
	if err := json.Unmarshal(legacy, &m); err != nil {
		t.Fatalf("老字段应被忽略: %v", err)
	}
	if m.Region != "US" || m.City != "洛杉矶" {
		t.Fatalf("解析结果 %+v", m)
	}
}
