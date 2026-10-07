package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// fakeNodeOps 记下被要求做了什么,并回放事先安排好的结果。
type fakeNodeOps struct {
	installs   int
	gotVersion string
	version    string
	err        error
}

func (f *fakeNodeOps) DeployNode(context.Context, model.Node, time.Time) error { return nil }

func (f *fakeNodeOps) InstallNode(_ context.Context, _ model.Node, version string) (string, error) {
	f.installs++
	f.gotVersion = version
	return f.version, f.err
}

func installPath(n model.Node, version string) string {
	p := "/api/nodes/" + strconv.FormatInt(n.ID, 10) + "/install"
	if version != "" {
		p += "?version=" + version
	}
	return p
}

func TestInstallNodeTellsCoreApartFromConfig(t *testing.T) {
	// 核心已经推上去了、只是服务没起来 —— 这两种失败该做的事不一样,接口得分开说。
	ops := &fakeNodeOps{version: "v26.3.27", err: errors.New("配置通过了校验但服务没起来")}
	e := newEnvWith(t, ops)
	n := e.createNode("hk", "node.example.com")

	code, data := e.api("POST", installPath(n, ""), nil)
	if code != http.StatusBadGateway {
		t.Fatalf("失败时该回 502,得到 %d %s", code, data)
	}
	body := string(data)
	if !strings.Contains(body, "v26.3.27") {
		t.Errorf("得说清核心已经换成了哪个版本,响应里没有:%s", body)
	}
	if !strings.Contains(body, "配置通过了校验但服务没起来") {
		t.Errorf("节点上的原始错误被吃掉了:%s", body)
	}
}

func TestInstallNodeReportsPlainFailureWhenNothingWasPushed(t *testing.T) {
	// version 为空表示核心还在半路就失败了(读不出平台、下载不动),此时不该
	// 冒出"核心已经换成"这种话,否则使用者会去节点上找一个根本没装上的核心。
	ops := &fakeNodeOps{err: errors.New("节点上是不认识的架构 mips")}
	e := newEnvWith(t, ops)
	n := e.createNode("hk", "node.example.com")

	code, data := e.api("POST", installPath(n, ""), nil)
	if code != http.StatusBadGateway {
		t.Fatalf("失败时该回 502,得到 %d %s", code, data)
	}
	body := string(data)
	if !strings.Contains(body, "mips") {
		t.Errorf("原始错误被吃掉了:%s", body)
	}
	if strings.Contains(body, "核心已经换成") {
		t.Errorf("一个字节都没推上去,却说核心换过了:%s", body)
	}
}

func TestInstallNodeReturnsVersionAndPassesXrayVersionThrough(t *testing.T) {
	ops := &fakeNodeOps{version: "v26.3.27"}
	e := newEnvWith(t, ops)
	n := e.createNode("hk", "node.example.com")

	code, data := e.api("POST", installPath(n, "v26.3.27"), nil)
	if code != http.StatusOK {
		t.Fatalf("成功时该回 200,得到 %d %s", code, data)
	}
	if ops.gotVersion != "v26.3.27" {
		t.Errorf("--xray-version 没传到控制面,拿到的是 %q", ops.gotVersion)
	}
	res := decodeJSON[map[string]string](t, data)
	if res["version"] != "v26.3.27" {
		t.Errorf("响应里该带上实际装上的版本,得到 %q", res["version"])
	}
}

func TestInstallNodeWithoutOpsSaysSo(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("hk", "node.example.com")

	code, data := e.api("POST", installPath(n, ""), nil)
	if code != http.StatusNotImplemented {
		t.Fatalf("没接上节点通道该回 501,得到 %d %s", code, data)
	}
}

func TestInstallNodeOnMissingNodeIsNotFound(t *testing.T) {
	e := newEnvWith(t, &fakeNodeOps{})

	code, data := e.api("POST", "/api/nodes/999/install", nil)
	if code != http.StatusNotFound {
		t.Fatalf("节点不存在该回 404,得到 %d %s", code, data)
	}
}
