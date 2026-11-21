package store

import (
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

func win(ip string, lat int) model.CDNResult {
	return model.CDNResult{IP: ip, Reachable: true, LatencyMs: lat, Edge: true, Colo: "SJC"}
}

func fail(ip string) model.CDNResult {
	return model.CDNResult{IP: ip} // TCP 不通或边缘验证失败:成败结论都带回(UC-24)
}

// AC-01:miss 语义 —— 通过的 miss 清零刷新延迟;池内成员没通过结论(失败项
// 或缺席)miss+1;edge=false 的新 IP 不入池。
func TestSaveCDNResultsMissSemantics(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()

	// 第一轮:a、b 通过入池。
	pool, err := s.SaveCDNResults([]model.CDNResult{win("1.1.1.1", 100), win("2.2.2.2", 50)}, now, 2, 30)
	if err != nil {
		t.Fatalf("第一轮落库: %v", err)
	}
	if len(pool) != 2 {
		t.Fatalf("池应有 2 个,实际 %d", len(pool))
	}

	// 第二轮:a 复检通过且延迟变了,b 回报失败,新 IP c 失败、新 IP d 通过。
	pool, err = s.SaveCDNResults([]model.CDNResult{
		win("1.1.1.1", 80), fail("2.2.2.2"), fail("9.9.9.9"), win("4.4.4.4", 30),
	}, now.Add(time.Hour), 2, 30)
	if err != nil {
		t.Fatalf("第二轮落库: %v", err)
	}
	byIP := map[string]model.CDNIP{}
	for _, e := range pool {
		byIP[e.IP] = e
	}
	if len(pool) != 3 {
		t.Fatalf("池应有 3 个(a、b、d),实际 %d", len(pool))
	}
	if byIP["1.1.1.1"].LatencyMs != 80 || byIP["1.1.1.1"].Misses != 0 {
		t.Errorf("a 应刷新延迟为 80 且 miss 清零,实际 %+v", byIP["1.1.1.1"])
	}
	if byIP["2.2.2.2"].Misses != 1 {
		t.Errorf("b 回报失败应 miss=1,实际 %d", byIP["2.2.2.2"].Misses)
	}
	if _, ok := byIP["9.9.9.9"]; ok {
		t.Error("edge=false 的新 IP c 不应入池")
	}
	if byIP["4.4.4.4"].Misses != 0 {
		t.Errorf("d 应通过入池 miss=0,实际 %+v", byIP["4.4.4.4"])
	}
}

// AC-03:连续 miss 超过 maxMisses 出池。
func TestSaveCDNResultsEvictAfterMaxMisses(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()

	if _, err := s.SaveCDNResults([]model.CDNResult{win("1.1.1.1", 100), win("2.2.2.2", 50)}, now, 2, 30); err != nil {
		t.Fatalf("第一轮: %v", err)
	}
	// b 连续 3 轮失败(maxMisses=2:第 3 次 miss 即出池)。
	for i := 1; i <= 3; i++ {
		pool, err := s.SaveCDNResults([]model.CDNResult{win("1.1.1.1", 100), fail("2.2.2.2")}, now.Add(time.Duration(i)*time.Hour), 2, 30)
		if err != nil {
			t.Fatalf("第 %d 轮: %v", i+1, err)
		}
		byIP := map[string]model.CDNIP{}
		for _, e := range pool {
			byIP[e.IP] = e
		}
		if i <= 2 {
			if e, ok := byIP["2.2.2.2"]; !ok || e.Misses != i {
				t.Errorf("第 %d 轮后 b 应 miss=%d 仍在池,实际 %+v", i, i, e)
			}
		} else if _, ok := byIP["2.2.2.2"]; ok {
			t.Error("b 连续 miss 超阈应出池")
		}
	}
}

// 软上限:池只留 miss 少、延迟低的 maxPool 个。
func TestSaveCDNResultsMaxPool(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()

	var rs []model.CDNResult
	for i := 0; i < 5; i++ {
		rs = append(rs, win("1.0.0."+string(rune('1'+i)), 10+i*10))
	}
	pool, err := s.SaveCDNResults(rs, now, 2, 3)
	if err != nil {
		t.Fatalf("落库: %v", err)
	}
	if len(pool) != 3 {
		t.Fatalf("软上限 3,实际 %d", len(pool))
	}
	for i, want := range []string{"1.0.0.1", "1.0.0.2", "1.0.0.3"} {
		if pool[i].IP != want {
			t.Errorf("应留延迟最低的前 3,%d 位是 %s", i, pool[i].IP)
		}
	}
}

// 节流:CDNLastRun/MarkCDNRun 往返。settings 首次保存才有行,先建行再测;
// 行缺失时读出错由调用方按「从没跑过」处理(targets 里 err 分支)。
func TestCDNRunThrottle(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CDNLastRun(); err == nil {
		t.Fatal("settings 未初始化时读 last_run 应出错")
	}
	if err := s.SaveSettings(DefaultSettings()); err != nil {
		t.Fatalf("初始化 settings: %v", err)
	}
	if last, err := s.CDNLastRun(); err != nil || last.Unix() != 0 {
		t.Fatalf("初始应为 0(从没跑过),实际 %v err=%v", last, err)
	}
	now := time.Now()
	if err := s.MarkCDNRun(now); err != nil {
		t.Fatalf("记轮次: %v", err)
	}
	last, err := s.CDNLastRun()
	if err != nil {
		t.Fatalf("读轮次: %v", err)
	}
	if last.Unix() != now.Unix() {
		t.Errorf("last_run 应等于写入时间,实际 %v != %v", last, now)
	}
}
