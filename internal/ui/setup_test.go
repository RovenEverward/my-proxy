package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func TestSetupJobNormalFlow(t *testing.T) {
	j := newSetupJob()
	if j.id == "" || len(j.id) != 16 {
		t.Fatalf("id = %q, want 16 hex chars", j.id)
	}

	j.Step("准备")
	j.Line("第一行")
	j.Line("第二行\n第三行")
	j.Step("安装")
	j.Line("第四行")
	j.finish(&SetupResult{HostPort: "example.net:22", Connected: true}, nil)

	d := j.delta(0)
	if d.State != "done" {
		t.Fatalf("state = %q, want done", d.State)
	}
	if len(d.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(d.Steps))
	}
	if d.Steps[0].State != "done" || d.Steps[1].State != "done" {
		t.Fatalf("steps states = %q,%q, want done,done", d.Steps[0].State, d.Steps[1].State)
	}
	if want := []string{"第一行", "第二行", "第三行", "第四行"}; !equalStrings(d.Lines, want) {
		t.Fatalf("lines = %q, want %q", d.Lines, want)
	}
	if d.Result == nil || d.Result.HostPort != "example.net:22" {
		t.Fatalf("result = %+v", d.Result)
	}
	if d.Elapsed < 0 {
		t.Fatalf("elapsed = %v", d.Elapsed)
	}

	d2 := j.delta(d.Seq)
	if len(d2.Steps) != 0 || len(d2.Lines) != 0 {
		t.Fatalf("second delta should be empty: %+v", d2)
	}
	if d2.Error != "" {
		t.Fatalf("unexpected error %q", d2.Error)
	}
	b, err := json.Marshal(d2)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) >= 200 {
		t.Fatalf("empty delta json = %d bytes, want < 200: %s", len(b), b)
	}
}

func TestSetupJobLargeOutput(t *testing.T) {
	j := newSetupJob()
	j.Step("跑命令")
	const total = 5000
	for i := 0; i < total; i++ {
		j.Line(fmt.Sprintf("%05d:", i) + strings.Repeat("x", 244))
	}

	var got []string
	since := int64(0)
	pulls := 0
	for {
		pulls++
		if pulls > total+10 {
			t.Fatalf("too many pulls, stuck at since=%d", since)
		}
		d := j.delta(since)
		b, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		if len(b) >= setupMaxDelta {
			t.Fatalf("delta json = %d bytes, want < %d", len(b), setupMaxDelta)
		}
		if d.Seq < since {
			t.Fatalf("seq went backwards: %d -> %d", since, d.Seq)
		}
		got = append(got, d.Lines...)
		since = d.Seq
		if !d.More {
			break
		}
	}
	if len(got) != setupKeepLines {
		t.Fatalf("got %d lines, want %d", len(got), setupKeepLines)
	}
	seen := make(map[string]bool, len(got))
	for i, ln := range got {
		if seen[ln] {
			t.Fatalf("duplicate line %q", ln)
		}
		seen[ln] = true
		want := fmt.Sprintf("%05d:", total-setupKeepLines+i)
		if !strings.HasPrefix(ln, want) {
			t.Fatalf("line %d = %.10q, want prefix %q", i, ln, want)
		}
	}
}

func TestSetupJobTruncatesLongLine(t *testing.T) {
	j := newSetupJob()
	long := strings.Repeat("中", 500) // 1500 bytes
	j.Line(long)
	d := j.delta(0)
	if len(d.Lines) != 1 {
		t.Fatalf("lines = %d, want 1", len(d.Lines))
	}
	got := d.Lines[0]
	if !utf8.ValidString(got) {
		t.Fatalf("truncated line is not valid UTF-8: %q", got)
	}
	if len(got) > 300 {
		t.Fatalf("truncated line = %d bytes, want <= 300", len(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("truncated line should end with ellipsis: %q", got)
	}
}

func TestSetupJobFailure(t *testing.T) {
	j := newSetupJob()
	j.Step("安装公钥")
	j.Line("boom")
	j.finish(nil, errors.New("x"))

	d := j.delta(0)
	if d.State != "failed" {
		t.Fatalf("state = %q, want failed", d.State)
	}
	if d.Error != "x" {
		t.Fatalf("error = %q, want x", d.Error)
	}
	if len(d.Steps) != 1 || d.Steps[0].State != "fail" {
		t.Fatalf("steps = %+v, want one fail step", d.Steps)
	}
}

func TestSetupJobConcurrent(t *testing.T) {
	j := newSetupJob()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				j.Line(fmt.Sprintf("g%d-%d", g, i))
				_ = j.delta(0)
			}
		}(g)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			j.Step(fmt.Sprintf("s%d", i))
		}
		j.finish(&SetupResult{HostPort: "h"}, nil)
	}()
	wg.Wait()

	if d := j.delta(0); d.State != "done" {
		t.Fatalf("state = %q, want done", d.State)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
