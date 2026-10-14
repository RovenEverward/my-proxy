package cli

import (
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

func TestPickDevice(t *testing.T) {
	ds := []model.Device{{ID: 1, Name: "mac"}, {ID: 2, Name: "pc"}, {ID: 3, Name: "pc"}, {ID: 4, Name: "7"}}
	if d, err := pickDevice(ds, "1"); err != nil || d.ID != 1 {
		t.Fatalf("按 id: %+v %v", d, err)
	}
	if d, err := pickDevice(ds, "mac"); err != nil || d.ID != 1 {
		t.Fatalf("按名称: %+v %v", d, err)
	}
	if _, err := pickDevice(ds, "pc"); err == nil {
		t.Fatal("重名应报错")
	}
	if d, err := pickDevice(ds, "7"); err != nil || d.ID != 4 {
		t.Fatalf("数字名称(无同号 id)应按名称命中: %+v %v", d, err)
	}
	if _, err := pickDevice(ds, "nope"); err == nil {
		t.Fatal("找不到应报错")
	}
}
