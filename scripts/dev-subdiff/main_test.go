package main

import (
	"encoding/json"
	"gopkg.in/yaml.v3"
	"testing"
)

func yamlBytes(t *testing.T, s string) []byte {
	t.Helper()
	var n yaml.Node
	if e := yaml.Unmarshal([]byte(s), &n); e != nil {
		t.Fatal(e)
	}
	out, e := yaml.Marshal(&n)
	if e != nil {
		t.Fatal(e)
	}
	return out
}
func TestOnlyApprovedClashChanges(t *testing.T) {
	before := yamlBytes(t, "dns:\n  nameserver: [https://doh.pub/dns-query, https://dns.alidns.com/dns-query]\nproxy-groups: [{name: 手动切换, type: select, proxies: [test]}]\nproxies: [{name: test, server: 203.0.113.10}]\n")
	after := yamlBytes(t, "dns:\n  nameserver: ['https://1.1.1.1/dns-query#手动切换', 'https://8.8.8.8/dns-query#手动切换']\nproxy-groups: [{name: 手动切换, type: select, proxies: [test]}]\nproxies: [{name: test, server: 203.0.113.10}]\n")
	if !clash(before, after) {
		t.Fatal("批准的 DNS 变更被拒绝")
	}
	tampered := yamlBytes(t, "dns:\n  nameserver: ['https://1.1.1.1/dns-query#手动切换', 'https://8.8.8.8/dns-query#手动切换']\nproxy-groups: [{name: 手动切换, type: select, proxies: [test]}]\nproxies: [{name: test, server: 198.51.100.10}]\n")
	if clash(before, tampered) {
		t.Fatal("节点地址变化被掩盖")
	}
}
func TestOnlyApprovedSingBoxChanges(t *testing.T) {
	encode := func(s string) []byte {
		var v any
		if e := json.Unmarshal([]byte(s), &v); e != nil {
			t.Fatal(e)
		}
		b, _ := json.MarshalIndent(v, "", "  ")
		return b
	}
	before := encode(`{"outbounds":[{"tag":"proxy","type":"selector","default":"auto","outbounds":["auto","test"]}],"route":{"final":"proxy"}}`)
	after := encode(`{"outbounds":[{"tag":"proxy","type":"selector","outbounds":["test","auto"]}],"route":{"final":"proxy"}}`)
	if !singbox(before, after) {
		t.Fatal("手动默认被拒绝")
	}
	bad := encode(`{"outbounds":[{"tag":"proxy","type":"selector","outbounds":["other","auto"]}],"route":{"final":"proxy"}}`)
	if singbox(before, bad) {
		t.Fatal("候选变化被掩盖")
	}
}
