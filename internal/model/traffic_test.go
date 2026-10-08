package model

import "testing"

// sampleAt 造一条累计值为 up/down 的采样,时间不参与差值计算,固定即可。
func sampleAt(up, down int64) TrafficSample {
	return TrafficSample{Username: "alice", NodeID: 1, Uplink: up, Downlink: down}
}

// 第一次采到某个用户时,历史里没有基准,增量就是他此刻的累计值 —— 当成 0 的话,
// 控制面开始记账之前已经产生的用量会永远对不上账。
func TestTrafficDeltaFirstSampleIsFullCumulative(t *testing.T) {
	delta := sampleAt(700, 300).DeltaFrom(nil)
	if delta.Restarted {
		t.Fatal("没有上次采样不该被判成重启")
	}
	if delta.Uplink != 700 || delta.Downlink != 300 {
		t.Fatalf("首次采样应当取整笔累计值 700/300,实际 %d/%d", delta.Uplink, delta.Downlink)
	}
}

func TestTrafficDeltaIsDifferenceFromPrevious(t *testing.T) {
	prev := sampleAt(100, 200)
	delta := sampleAt(160, 260).DeltaFrom(&prev)
	if delta.Restarted {
		t.Fatal("累计值在涨,不该被判成重启")
	}
	if delta.Uplink != 60 || delta.Downlink != 60 {
		t.Fatalf("应当得到增量 60/60,实际 %d/%d", delta.Uplink, delta.Downlink)
	}
}

// 累计值没变就是没有新用量,不是「没采到」。
func TestTrafficDeltaZeroWhenUnchanged(t *testing.T) {
	prev := sampleAt(100, 200)
	delta := sampleAt(100, 200).DeltaFrom(&prev)
	if delta.Restarted {
		t.Fatal("值没变不该被判成重启")
	}
	if delta.Uplink != 0 || delta.Downlink != 0 {
		t.Fatalf("没变化时增量应当是 0,实际 %d/%d", delta.Uplink, delta.Downlink)
	}
}

// xray 的计数只增不减,回退只有一种解释:节点上的 xray 重启过,计数从零重来。
// 这时增量必须按 0 处理,否则会把用户已经用掉的量倒扣回去。
func TestTrafficDeltaDetectsRestart(t *testing.T) {
	prev := sampleAt(5000, 8000)
	delta := sampleAt(7, 3).DeltaFrom(&prev)
	if !delta.Restarted {
		t.Fatal("累计值回退没有被判成重启,这种账会静默消失")
	}
	if delta.Uplink != 0 || delta.Downlink != 0 {
		t.Fatalf("回退时增量应当按 0 处理,实际 %d/%d", delta.Uplink, delta.Downlink)
	}
}

// 只有一边回退也按重启处理:一次重启会把两个方向一起清零,只坏一边说明这个假设
// 不成立,那也该停下来看,而不是把负增量当成正常差值累加。
func TestTrafficDeltaDetectsOneSidedRollback(t *testing.T) {
	prev := sampleAt(5000, 8000)
	delta := sampleAt(6000, 100).DeltaFrom(&prev)
	if !delta.Restarted {
		t.Fatal("只有一个方向回退时也应当停下来报警,而不是把负增量累加进去")
	}
}
