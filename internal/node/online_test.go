package node

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// 配置没开 statsUserOnline 时 xray 的在线 API 只回 {},不能报成「没人在线」。
func TestOnlineIPsStatsOffIsAnError(t *testing.T) {
	r := &fakeRunner{fn: func(script string) (string, error) {
		if !strings.Contains(script, "statsUserOnline") {
			t.Fatalf("没先检查配置: %s", script)
		}
		return onlineStatsOffMarker + "\n", nil
	}}
	_, err := OnlineIPs(context.Background(), r, model.Node{Name: "la", Address: "203.0.113.10", ConfigPath: "/etc/mpx/xray/config.json"})
	if !errors.Is(err, ErrOnlineStatsOff) {
		t.Fatalf("err = %v, want ErrOnlineStatsOff", err)
	}
}

// xray 26 的真实返回:users 是计数名字符串,IP 要再用 statsonlineiplist 取。
func TestOnlineIPsParsesRealXrayShape(t *testing.T) {
	r := &fakeRunner{fn: func(script string) (string, error) {
		if strings.Contains(script, "statsgetallonlineusers") {
			return `{"users":["user>>>alice>>>online","user>>>bob>>>online"]}`, nil
		}
		if !strings.Contains(script, "-email 'alice'") || !strings.Contains(script, "-email 'bob'") {
			t.Fatalf("没按用户取 IP: %s", script)
		}
		return `{"name":"user>>>alice>>>online","ips":{"198.51.100.7":1760000000,"203.0.113.9":1760000001}}
` + onlineSep + `
{"name":"user>>>bob>>>online","ips":{"198.51.100.8":1760000002}}
` + onlineSep + "\n", nil
	}}
	got, err := OnlineIPs(context.Background(), r, model.Node{Name: "la", Address: "203.0.113.10"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{"alice": {"198.51.100.7", "203.0.113.9"}, "bob": {"198.51.100.8"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if len(r.calls) != 2 {
		t.Fatalf("应该两次 SSH,实际 %d", len(r.calls))
	}
}

func TestOnlineIPsNobodyOnline(t *testing.T) {
	r := &fakeRunner{fn: func(string) (string, error) { return "{}\n", nil }}
	got, err := OnlineIPs(context.Background(), r, model.Node{Name: "la", Address: "203.0.113.10"})
	if err != nil || len(got) != 0 || len(r.calls) != 1 {
		t.Fatalf("got %v err %v calls %d", got, err, len(r.calls))
	}
}
