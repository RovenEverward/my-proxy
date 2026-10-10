package store

import (
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// newTestStore 在 t.TempDir() 下落一个临时库,测试结束自动关掉。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatalf("打开测试数据库: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("关闭测试数据库: %v", err)
		}
	})
	return s
}

func newTestNode(t *testing.T, s *Store, name string) *model.Node {
	t.Helper()
	n := &model.Node{
		Name:        name,
		Address:     "203.0.113.7",
		SSHPort:     2222,
		SSHUser:     "ops",
		APIPort:     10085,
		ConfigPath:  "/etc/xray/config.json",
		ServiceName: "xray",
	}
	if err := s.CreateNode(n); err != nil {
		t.Fatalf("创建节点: %v", err)
	}
	if n.ID == 0 {
		t.Fatal("CreateNode 没有回填 ID")
	}
	return n
}

func newTestInbound(t *testing.T, s *Store, nodeID int64, tag string) *model.Inbound {
	t.Helper()
	i := &model.Inbound{
		NodeID:   nodeID,
		Tag:      tag,
		Protocol: model.ProtocolVLESS,
		Port:     443,
		Network:  model.NetworkTCP,
		Security: model.SecurityReality,
	}
	if err := s.CreateInbound(i); err != nil {
		t.Fatalf("创建入站: %v", err)
	}
	if i.ID == 0 {
		t.Fatal("CreateInbound 没有回填 ID")
	}
	return i
}

func newTestHost(t *testing.T, s *Store, inboundID int64, remark string) *model.Host {
	t.Helper()
	h := &model.Host{InboundID: inboundID, Remark: remark, Address: "cdn.example.com", Port: 8443}
	if err := s.CreateHost(h); err != nil {
		t.Fatalf("创建 Host: %v", err)
	}
	if h.ID == 0 {
		t.Fatal("CreateHost 没有回填 ID")
	}
	return h
}

func newTestUser(t *testing.T, s *Store, username string) *model.User {
	t.Helper()
	u := &model.User{Username: username, Key: "k-" + username, SubToken: "t-" + username, Enabled: true}
	if err := s.CreateUser(u); err != nil {
		t.Fatalf("创建用户: %v", err)
	}
	if u.ID == 0 {
		t.Fatal("CreateUser 没有回填 ID")
	}
	if u.CreatedAt.IsZero() {
		t.Fatal("CreateUser 没有回填 CreatedAt")
	}
	return u
}

// sameStrings 认为 nil 与空切片相等:库里空切片存成 "[]",读回来是 nil,两者
// 都表示"没有值"。
func sameStrings(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

func sameInt64s(a, b []int64) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// ---- Node ----

func TestNodeCRUD(t *testing.T) {
	s := newTestStore(t)

	n := newTestNode(t, s, "hk-1")

	got, err := s.GetNode(n.ID)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if !reflect.DeepEqual(got, n) {
		t.Fatalf("GetNode 往返不一致:\n got %+v\nwant %+v", got, n)
	}

	n2 := newTestNode(t, s, "jp-1")
	list, err := s.ListNodes()
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(list) != 2 || list[0].ID != n.ID || list[1].ID != n2.ID {
		t.Fatalf("ListNodes 结果不对: %+v", list)
	}

	n.Address = "198.51.100.9"
	n.SSHPort = 22
	n.SSHUser = ""
	n.ServiceName = "xray-custom"
	if err := s.UpdateNode(n); err != nil {
		t.Fatalf("UpdateNode: %v", err)
	}
	got, err = s.GetNode(n.ID)
	if err != nil {
		t.Fatalf("更新后 GetNode: %v", err)
	}
	if !reflect.DeepEqual(got, n) {
		t.Fatalf("UpdateNode 后不一致:\n got %+v\nwant %+v", got, n)
	}

	if err := s.DeleteNode(n.ID); err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}
	if _, err := s.GetNode(n.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除后 GetNode 应返回 ErrNotFound,实际 %v", err)
	}
	if err := s.DeleteNode(n.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("重复 DeleteNode 应返回 ErrNotFound,实际 %v", err)
	}
	if err := s.UpdateNode(n); !errors.Is(err, ErrNotFound) {
		t.Fatalf("更新不存在的节点应返回 ErrNotFound,实际 %v", err)
	}
}

func TestNodeNameUnique(t *testing.T) {
	s := newTestStore(t)
	newTestNode(t, s, "same-name")

	err := s.CreateNode(&model.Node{Name: "same-name"})
	if err == nil {
		t.Fatal("重名节点应当插入失败")
	}
}

// ---- Inbound ----

func TestInboundCRUD(t *testing.T) {
	s := newTestStore(t)
	node := newTestNode(t, s, "hk-1")

	i := newTestInbound(t, s, node.ID, "vless-reality")
	i.WSPath = "/ws"
	i.GRPCService = "grpc-svc"
	i.SNI = "www.microsoft.com"
	i.Fingerprint = "chrome"
	i.RealityDest = "www.microsoft.com:443"
	i.RealityPriv = "priv-key"
	i.RealityPub = "pub-key"
	i.Flow = "xtls-rprx-vision"
	i.ALPN = []string{"h2", "http/1.1"}
	i.ShortIDs = []string{"ab12", "cd34"}
	if err := s.UpdateInbound(i); err != nil {
		t.Fatalf("UpdateInbound: %v", err)
	}

	got, err := s.GetInbound(i.ID)
	if err != nil {
		t.Fatalf("GetInbound: %v", err)
	}
	if !reflect.DeepEqual(got, i) {
		t.Fatalf("GetInbound 往返不一致:\n got %+v\nwant %+v", got, i)
	}

	other := newTestInbound(t, s, node.ID, "trojan-tls")
	other.Protocol = model.ProtocolTrojan
	other.Security = model.SecurityTLS
	other.Network = model.NetworkWS
	if err := s.UpdateInbound(other); err != nil {
		t.Fatalf("UpdateInbound: %v", err)
	}

	list, err := s.ListInbounds(node.ID)
	if err != nil {
		t.Fatalf("ListInbounds: %v", err)
	}
	if len(list) != 2 || list[0].ID != i.ID || list[1].ID != other.ID {
		t.Fatalf("ListInbounds(nodeID) 结果不对: %+v", list)
	}
	all, err := s.ListInbounds(0)
	if err != nil {
		t.Fatalf("ListInbounds(0): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListInbounds(0) 应返回全部 2 条,实际 %d", len(all))
	}

	if err := s.DeleteInbound(i.ID); err != nil {
		t.Fatalf("DeleteInbound: %v", err)
	}
	if _, err := s.GetInbound(i.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除后 GetInbound 应返回 ErrNotFound,实际 %v", err)
	}
	if err := s.UpdateInbound(i); !errors.Is(err, ErrNotFound) {
		t.Fatalf("更新不存在的入站应返回 ErrNotFound,实际 %v", err)
	}
}

// TestInboundSliceRoundTrip 覆盖切片字段 nil / 空切片 / 多元素的往返。
func TestInboundSliceRoundTrip(t *testing.T) {
	s := newTestStore(t)
	node := newTestNode(t, s, "hk-1")

	cases := []struct {
		name     string
		alpn     []string
		shortIDs []string
	}{
		{"都为 nil", nil, nil},
		{"空切片", []string{}, []string{}},
		{"单元素", []string{"h2"}, []string{"abcd"}},
		{"多元素", []string{"h2", "http/1.1"}, []string{"ab12", "cd34", "ef56"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			i := &model.Inbound{
				NodeID:   node.ID,
				Tag:      "in-" + tc.name,
				Protocol: model.ProtocolVLESS,
				Port:     443,
				Network:  model.NetworkTCP,
				Security: model.SecurityTLS,
				ALPN:     tc.alpn,
				ShortIDs: tc.shortIDs,
			}
			if err := s.CreateInbound(i); err != nil {
				t.Fatalf("CreateInbound: %v", err)
			}
			got, err := s.GetInbound(i.ID)
			if err != nil {
				t.Fatalf("GetInbound: %v", err)
			}
			if !sameStrings(got.ALPN, tc.alpn) {
				t.Errorf("ALPN 往返不一致: got %#v want %#v", got.ALPN, tc.alpn)
			}
			if !sameStrings(got.ShortIDs, tc.shortIDs) {
				t.Errorf("ShortIDs 往返不一致: got %#v want %#v", got.ShortIDs, tc.shortIDs)
			}
			// 空切片不应被读成含空字符串的切片。
			for _, v := range append(append([]string{}, got.ALPN...), got.ShortIDs...) {
				if v == "" {
					t.Errorf("切片里出现了空字符串: %#v / %#v", got.ALPN, got.ShortIDs)
				}
			}
		})
	}
}

func TestCreateInboundWithMissingNodeFails(t *testing.T) {
	s := newTestStore(t)
	err := s.CreateInbound(&model.Inbound{
		NodeID:   999,
		Tag:      "orphan",
		Protocol: model.ProtocolVLESS,
		Port:     443,
		Network:  model.NetworkTCP,
		Security: model.SecurityTLS,
	})
	if err == nil {
		t.Fatal("node_id 不存在时应当被外键拒绝")
	}
}

// ---- Host ----

func TestHostCRUD(t *testing.T) {
	s := newTestStore(t)
	node := newTestNode(t, s, "hk-1")
	in := newTestInbound(t, s, node.ID, "vless-reality")

	h := newTestHost(t, s, in.ID, "香港 01")
	h.SNI = "cdn.example.com"
	h.Host = "cdn.example.com"
	h.Path = "/ws"
	h.ALPN = []string{"h2"}
	if err := s.UpdateHost(h); err != nil {
		t.Fatalf("UpdateHost: %v", err)
	}

	got, err := s.GetHost(h.ID)
	if err != nil {
		t.Fatalf("GetHost: %v", err)
	}
	if !reflect.DeepEqual(got, h) {
		t.Fatalf("GetHost 往返不一致:\n got %+v\nwant %+v", got, h)
	}

	// 同一入站下的第二个 Host,并验证 ALPN 为 nil 时也能往返。
	h2 := &model.Host{InboundID: in.ID, Remark: "香港 02", Address: "198.51.100.9"}
	if err := s.CreateHost(h2); err != nil {
		t.Fatalf("CreateHost: %v", err)
	}
	gotH2, err := s.GetHost(h2.ID)
	if err != nil {
		t.Fatalf("GetHost: %v", err)
	}
	if !sameStrings(gotH2.ALPN, nil) {
		t.Fatalf("ALPN 为 nil 时读回来应是空,实际 %#v", gotH2.ALPN)
	}

	list, err := s.ListHosts(in.ID)
	if err != nil {
		t.Fatalf("ListHosts: %v", err)
	}
	if len(list) != 2 || list[0].ID != h.ID || list[1].ID != h2.ID {
		t.Fatalf("ListHosts(inboundID) 结果不对: %+v", list)
	}
	if all, err := s.ListHosts(0); err != nil || len(all) != 2 {
		t.Fatalf("ListHosts(0) 应返回 2 条,实际 %d,err=%v", len(all), err)
	}

	if err := s.DeleteHost(h.ID); err != nil {
		t.Fatalf("DeleteHost: %v", err)
	}
	if _, err := s.GetHost(h.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除后 GetHost 应返回 ErrNotFound,实际 %v", err)
	}
	if err := s.UpdateHost(h); !errors.Is(err, ErrNotFound) {
		t.Fatalf("更新不存在的 Host 应返回 ErrNotFound,实际 %v", err)
	}
}

// ---- 级联删除 ----

func TestDeleteNodeCascadesToInboundsAndHosts(t *testing.T) {
	s := newTestStore(t)
	node := newTestNode(t, s, "hk-1")
	in1 := newTestInbound(t, s, node.ID, "in-1")
	in2 := newTestInbound(t, s, node.ID, "in-2")
	h1 := newTestHost(t, s, in1.ID, "host-1")
	h2 := newTestHost(t, s, in2.ID, "host-2")

	if err := s.DeleteNode(node.ID); err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}

	for _, id := range []int64{in1.ID, in2.ID} {
		if _, err := s.GetInbound(id); !errors.Is(err, ErrNotFound) {
			t.Errorf("入站 %d 应随节点一起删掉,实际 %v", id, err)
		}
	}
	for _, id := range []int64{h1.ID, h2.ID} {
		if _, err := s.GetHost(id); !errors.Is(err, ErrNotFound) {
			t.Errorf("Host %d 应随节点一起删掉,实际 %v", id, err)
		}
	}
	if list, err := s.ListInbounds(0); err != nil || len(list) != 0 {
		t.Errorf("级联后不应还有入站: %d 条,err=%v", len(list), err)
	}
	if list, err := s.ListHosts(0); err != nil || len(list) != 0 {
		t.Errorf("级联后不应还有 Host: %d 条,err=%v", len(list), err)
	}
}

func TestDeleteInboundCascadesToHosts(t *testing.T) {
	s := newTestStore(t)
	node := newTestNode(t, s, "hk-1")
	in := newTestInbound(t, s, node.ID, "in-1")
	h := newTestHost(t, s, in.ID, "host-1")
	other := newTestInbound(t, s, node.ID, "in-2")
	keep := newTestHost(t, s, other.ID, "host-2")

	if err := s.DeleteInbound(in.ID); err != nil {
		t.Fatalf("DeleteInbound: %v", err)
	}
	if _, err := s.GetHost(h.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Host 应随入站一起删掉,实际 %v", err)
	}
	// 别的入站及其 Host 不受影响。
	if _, err := s.GetInbound(other.ID); err != nil {
		t.Fatalf("另一个入站不该被删: %v", err)
	}
	if _, err := s.GetHost(keep.ID); err != nil {
		t.Fatalf("另一个入站下的 Host 不该被删: %v", err)
	}
}

// ---- User ----

func TestUserCRUD(t *testing.T) {
	s := newTestStore(t)
	in1 := newTestInbound(t, s, newTestNode(t, s, "hk-1").ID, "in-1")
	in2 := newTestInbound(t, s, newTestNode(t, s, "jp-1").ID, "in-2")

	expire := time.Date(2027, 3, 1, 12, 0, 0, 0, time.UTC)
	u := &model.User{
		Username:      "alice",
		Key:           "seed-key",
		SubToken:      "sub-token-alice",
		Enabled:       true,
		DataLimit:     100 << 30,
		UsedTraffic:   1 << 30,
		ExpireAt:      &expire,
		ResetStrategy: model.ResetMonthly,
		InboundIDs:    []int64{in1.ID, in2.ID},
		Note:          "测试用户",
	}
	if err := s.CreateUser(u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	got, err := s.GetUser("alice")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if !reflect.DeepEqual(got, u) {
		t.Fatalf("GetUser 往返不一致:\n got %+v\nwant %+v", got, u)
	}

	// 过期时间为 nil 的用户。
	u2 := &model.User{Username: "bob", Key: "seed-bob", SubToken: "sub-token-bob", Enabled: false}
	if err := s.CreateUser(u2); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	gotBob, err := s.GetUser("bob")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if gotBob.ExpireAt != nil {
		t.Fatalf("ExpireAt 为 nil 时读回来应是 nil,实际 %v", gotBob.ExpireAt)
	}
	if gotBob.Enabled {
		t.Fatal("Enabled=false 读回来变成了 true")
	}
	if !sameInt64s(gotBob.InboundIDs, nil) {
		t.Fatalf("InboundIDs 为 nil 时读回来应是空,实际 %#v", gotBob.InboundIDs)
	}

	list, err := s.ListUsers()
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(list) != 2 || list[0].Username != "alice" || list[1].Username != "bob" {
		t.Fatalf("ListUsers 结果不对: %+v", list)
	}

	// Update:改名不行,改其余字段生效。
	u.DataLimit = 200 << 30
	u.Enabled = false
	u.ExpireAt = nil
	u.Note = "已停用"
	if err := s.UpdateUser(u); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	got, err = s.GetUser("alice")
	if err != nil {
		t.Fatalf("更新后 GetUser: %v", err)
	}
	if !reflect.DeepEqual(got, u) {
		t.Fatalf("UpdateUser 后不一致:\n got %+v\nwant %+v", got, u)
	}
	// created_at 不被 Update 改写。
	if !got.CreatedAt.Equal(u.CreatedAt) {
		t.Fatalf("UpdateUser 改动了 created_at: got %v want %v", got.CreatedAt, u.CreatedAt)
	}

	if err := s.DeleteUser("bob"); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, err := s.GetUser("bob"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除后 GetUser 应返回 ErrNotFound,实际 %v", err)
	}
	if err := s.UpdateUser(u2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("更新不存在的用户应返回 ErrNotFound,实际 %v", err)
	}
}

// TestUserInboundIDsRoundTrip 覆盖 InboundIDs 的 nil / 空切片 / 多元素往返。
func TestUserInboundIDsRoundTrip(t *testing.T) {
	s := newTestStore(t)
	cases := []struct {
		name string
		ids  []int64
	}{
		{"nil", nil},
		{"空切片", []int64{}},
		{"多元素", []int64{1, 2, 3}},
	}
	for idx, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			username := "u" + string(rune('a'+idx))
			u := &model.User{Username: username, Key: "k", SubToken: "t-" + username, InboundIDs: tc.ids}
			if err := s.CreateUser(u); err != nil {
				t.Fatalf("CreateUser: %v", err)
			}
			got, err := s.GetUser(username)
			if err != nil {
				t.Fatalf("GetUser: %v", err)
			}
			if !sameInt64s(got.InboundIDs, tc.ids) {
				t.Fatalf("InboundIDs 往返不一致: got %#v want %#v", got.InboundIDs, tc.ids)
			}
		})
	}
}

func TestUsernameUnique(t *testing.T) {
	s := newTestStore(t)
	newTestUser(t, s, "alice")

	err := s.CreateUser(&model.User{Username: "alice"})
	if err == nil {
		t.Fatal("重名用户应当插入失败")
	}
}

func TestGetUserBySubToken(t *testing.T) {
	s := newTestStore(t)
	u := &model.User{Username: "alice", Key: "k", SubToken: "secret-token"}
	if err := s.CreateUser(u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	got, err := s.GetUserBySubToken("secret-token")
	if err != nil {
		t.Fatalf("GetUserBySubToken: %v", err)
	}
	if got.Username != "alice" {
		t.Fatalf("按 token 找到的用户不对: %+v", got)
	}

	if _, err := s.GetUserBySubToken("no-such-token"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("token 不存在应返回 ErrNotFound,实际 %v", err)
	}
	// 空 token 不能撞上 token 为空的用户。
	empty := &model.User{Username: "no-token", Key: "k2"}
	if err := s.CreateUser(empty); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, err := s.GetUserBySubToken(""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("空 token 应返回 ErrNotFound,实际 %v", err)
	}
}

// ---- 流量 ----

func TestAddUserTrafficAccumulates(t *testing.T) {
	s := newTestStore(t)
	newTestUser(t, s, "alice")

	for i := 0; i < 5; i++ {
		if err := s.AddUserTraffic("alice", 100, 10); err != nil {
			t.Fatalf("第 %d 次 AddUserTraffic: %v", i+1, err)
		}
	}
	got, err := s.GetUser("alice")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if want := int64(5 * 110); got.UsedTraffic != want {
		t.Fatalf("流量累加不对: got %d want %d", got.UsedTraffic, want)
	}

	if err := s.AddUserTraffic("nobody", 1, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("给不存在的用户加流量应返回 ErrNotFound,实际 %v", err)
	}
}

// TestAddUserTrafficConcurrent 验证连接池设为单连接后,并发累加不会丢计数、
// 也不会因 database is locked 失败。这正是 SetMaxOpenConns(1) 想买到的东西。
func TestAddUserTrafficConcurrent(t *testing.T) {
	s := newTestStore(t)
	newTestUser(t, s, "alice")

	const (
		goroutines = 8
		perRoutine = 50
	)
	var wg sync.WaitGroup
	errCh := make(chan error, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perRoutine; i++ {
				if err := s.AddUserTraffic("alice", 1, 2); err != nil {
					errCh <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("并发 AddUserTraffic 失败: %v", err)
	}

	got, err := s.GetUser("alice")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if want := int64(goroutines * perRoutine * 3); got.UsedTraffic != want {
		t.Fatalf("并发累加丢了计数: got %d want %d", got.UsedTraffic, want)
	}
	if want := int64(goroutines * perRoutine * 3); got.LifetimeUsedTraffic != want {
		t.Fatalf("并发累加丢了累计计数: got %d want %d", got.LifetimeUsedTraffic, want)
	}
}

func TestResetUserTraffic(t *testing.T) {
	s := newTestStore(t)
	newTestUser(t, s, "alice")
	if err := s.AddUserTraffic("alice", 100, 23); err != nil {
		t.Fatalf("AddUserTraffic: %v", err)
	}

	resetAt := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	if err := s.ResetUserTraffic("alice", resetAt); err != nil {
		t.Fatalf("ResetUserTraffic: %v", err)
	}
	got, err := s.GetUser("alice")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if got.UsedTraffic != 0 {
		t.Fatalf("流量没有归零: %d", got.UsedTraffic)
	}
	if got.LifetimeUsedTraffic != 123 {
		t.Fatalf("累计用量不该被重置: %d", got.LifetimeUsedTraffic)
	}
	if got.TrafficResetAt == nil || !got.TrafficResetAt.Equal(resetAt) {
		t.Fatalf("周期起算点不对: %v", got.TrafficResetAt)
	}

	if err := s.ResetUserTraffic("nobody", resetAt); !errors.Is(err, ErrNotFound) {
		t.Fatalf("重置不存在的用户应返回 ErrNotFound,实际 %v", err)
	}
}

// ---- Settings ----

func TestSettingsDefaultOnEmptyDB(t *testing.T) {
	s := newTestStore(t)

	got, err := s.GetSettings()
	if err != nil {
		t.Fatalf("空库上 GetSettings 不该报错: %v", err)
	}
	want := DefaultSettings()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("默认设置不对:\n got %+v\nwant %+v", got, want)
	}
	if got.ProfileTitle != "{USERNAME}" {
		t.Errorf("ProfileTitle 默认值应为 {USERNAME},实际 %q", got.ProfileTitle)
	}
	if got.UpdateInterval != 24 {
		t.Errorf("UpdateInterval 默认值应为 24,实际 %d", got.UpdateInterval)
	}
	// 规则现在有内置默认:空库不该是一张空规则表,否则装在客户端那边的订阅
	// 只会拿到分享链接(见 model.BuiltinRules)。
	if !reflect.DeepEqual(got.Rules, model.BuiltinRules()) {
		t.Errorf("Rules 默认值应为内置规则,实际 %#v", got.Rules)
	}
	if got.SubURLPrefix != "" {
		t.Errorf("SubURLPrefix 默认值应为空,实际 %q", got.SubURLPrefix)
	}
}

func TestSaveSettingsRoundTrip(t *testing.T) {
	s := newTestStore(t)

	st := &model.Settings{
		SubURLPrefix:   "https://sub.example.com",
		ProfileTitle:   "我的代理 {USERNAME}",
		SupportLink:    "https://t.me/support",
		UpdateInterval: 6,
		Rules: []model.SubscriptionRule{
			{Pattern: "(?i)clash", Result: model.FormatClashMeta},
			{Pattern: "(?i)sing-box", Result: model.FormatSingBox},
		},
	}
	if err := s.SaveSettings(st); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	got, err := s.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if !reflect.DeepEqual(got, st) {
		t.Fatalf("SaveSettings 后读回不一致:\n got %+v\nwant %+v", got, st)
	}
	if len(got.Rules) != 2 || got.Rules[1].Result != model.FormatSingBox {
		t.Fatalf("Rules 读回不对: %+v", got.Rules)
	}

	// 再存一次应覆盖同一行,而不是插出第二行。
	st.ProfileTitle = "改过的标题"
	st.Rules = nil
	if err := s.SaveSettings(st); err != nil {
		t.Fatalf("第二次 SaveSettings: %v", err)
	}
	got, err = s.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	// 存 nil 规则落库是 "[]",读回来照例回落到内置默认 —— 与 profile_title
	// 一样被这次保存覆盖了,只是覆盖的结果又变回默认那套规则。
	if got.ProfileTitle != "改过的标题" {
		t.Fatalf("第二次保存没覆盖: %+v", got)
	}
	if !reflect.DeepEqual(got.Rules, model.BuiltinRules()) {
		t.Fatalf("清空规则后应回落到内置默认,实际 %#v", got.Rules)
	}
	var rows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM settings`).Scan(&rows); err != nil {
		t.Fatalf("统计 settings 行数: %v", err)
	}
	if rows != 1 {
		t.Fatalf("settings 应始终只有一行,实际 %d 行", rows)
	}

	if err := s.SaveSettings(nil); err == nil {
		t.Fatal("保存 nil 设置应当报错")
	}
}

// 老库升级上来的样子:settings 行已经存在,rules 列是 '[]'。这时必须也拿到内置
// 规则,否则「新装的库有规则、升级上来的库没有」—— 同一个版本两种行为,而且
// 老用户正是最需要它的人(他们的客户端本来就没被认出来)。
func TestGetSettingsFallsBackToBuiltinRulesOnEmptyColumn(t *testing.T) {
	s := newTestStore(t)

	// 直接写一行 rules='[]' 的设置,绕开 SaveSettings(它会带上当次传入的规则)。
	if _, err := s.db.Exec(
		`INSERT INTO settings (id, sub_url_prefix, profile_title, support_link,
		                       update_interval, webhook_url, notify_days_left,
		                       notify_usage_percent, rules, templates)
		 VALUES (1, 'https://old.example.com', '老的订阅', '', 24, '', 3, 80, '[]', '{}')`); err != nil {
		t.Fatalf("造老库设置行: %v", err)
	}

	got, err := s.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if !reflect.DeepEqual(got.Rules, model.BuiltinRules()) {
		t.Fatalf("rules 列为空时应回落到内置默认,实际 %#v", got.Rules)
	}
	// 其余字段要照旧读出来,回落不该影响它们。
	if got.SubURLPrefix != "https://old.example.com" || got.ProfileTitle != "老的订阅" {
		t.Errorf("回落规则时改动了别的字段: %+v", got)
	}

	// 配过的规则不能被默认值顶掉。
	custom := []model.SubscriptionRule{{Pattern: `(?i)我的客户端`, Result: model.FormatSingBox}}
	got.Rules = custom
	if err := s.SaveSettings(got); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	again, err := s.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if !reflect.DeepEqual(again.Rules, custom) {
		t.Fatalf("配过的规则被默认值顶掉了: %#v", again.Rules)
	}
}

func TestSaveSettingsTemplatesRoundTrip(t *testing.T) {
	s := newTestStore(t)

	st := &model.Settings{
		ProfileTitle: "tpl",
		Templates: map[string]string{
			"clash-meta": "proxies: []\n",
			"sing-box":   `{"outbounds":[]}`,
		},
	}
	if err := s.SaveSettings(st); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	got, err := s.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if got.Templates["clash-meta"] != "proxies: []\n" {
		t.Errorf("clash-meta 模板读回不对: %q", got.Templates["clash-meta"])
	}
	if got.Templates["sing-box"] != `{"outbounds":[]}` {
		t.Errorf("sing-box 模板读回不对: %q", got.Templates["sing-box"])
	}
	if got.TemplateFor(model.FormatXray) != "" {
		t.Errorf("没存过的 xray 模板应为空,实际 %q", got.TemplateFor(model.FormatXray))
	}

	// 删掉一个键再存,读回时那个键应消失。
	delete(got.Templates, "sing-box")
	if err := s.SaveSettings(got); err != nil {
		t.Fatalf("第二次 SaveSettings: %v", err)
	}
	got2, err := s.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if _, ok := got2.Templates["sing-box"]; ok {
		t.Error("删除的 sing-box 模板应消失")
	}
	if got2.Templates["clash-meta"] == "" {
		t.Error("未删除的 clash-meta 模板应保留")
	}
}

// ---- Open / 建表 ----

func TestOpenIsIdempotentAndPersistent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "control.db")

	s1, err := Open(path)
	if err != nil {
		t.Fatalf("首次 Open: %v", err)
	}
	newTestNode(t, s1, "hk-1")
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// 再打开同一个库:建表语句必须幂等,数据还在。
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("再次 Open: %v", err)
	}
	defer s2.Close()

	list, err := s2.ListNodes()
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(list) != 1 || list[0].Name != "hk-1" {
		t.Fatalf("重开后数据不对: %+v", list)
	}
}

func TestOpenEmptyPathFails(t *testing.T) {
	if _, err := Open(""); err == nil {
		t.Fatal("空路径应当报错")
	}
}

func TestForeignKeysEnabled(t *testing.T) {
	s := newTestStore(t)
	if err := s.checkForeignKeys(); err != nil {
		t.Fatalf("外键没开: %v", err)
	}
	var mode string
	if err := s.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("读 journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode 应为 wal,实际 %q", mode)
	}
}
