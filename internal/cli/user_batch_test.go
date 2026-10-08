package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// TestBatchPartialFailure 用假控制面让第二、三个名字回 500,断言批量命令:
//  1. 每个名字都有一行输出,成功的仍然执行了;
//  2. 末尾打「完成:1 个成功,2 个失败」;
//  3. 返回的错误是 errBatchFailed;
//  4. -o json 打的是数组,元素里有 ok:false 与 error;
//  5. 单名字时输出形状不变(还是原来那句人话)。
func TestBatchPartialFailure(t *testing.T) {
	type req struct{ method, username string }

	var mu sync.Mutex
	var reqs []req
	handler := func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = body
		username := strings.TrimPrefix(r.URL.Path, "/api/users/")
		mu.Lock()
		reqs = append(reqs, req{method: r.Method, username: username})
		mu.Unlock()
		if username == "bob" || username == "carol" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"故意的 500"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}
	fakeControlPlane(t, handler)

	// ---- 文本模式:三个名字,第二个第三个失败 ----
	out := captureStdout(t, func() {
		rootCmd.SetArgs([]string{"user", "rm", "alice", "bob", "carol"})
		defer rootCmd.SetArgs(nil)
		err := rootCmd.Execute()
		if err == nil {
			t.Fatal("批量有失败时应当返回 errBatchFailed")
		}
		if err != errBatchFailed {
			t.Fatalf("错误 = %v, want errBatchFailed", err)
		}
	})
	for _, want := range []string{
		"alice",          // 每个名字都有一行
		"bob",            //
		"carol",          //
		"完成:1 个成功,2 个失败", // 末尾汇总
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("输出里没有 %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "完成:0 个成功") || strings.Contains(out, "完成:3 个成功") {
		t.Fatalf("汇总数字不对:\n%s", out)
	}

	// ---- 成功的那个真的执行了,失败的也发出了请求 ----
	mu.Lock()
	gotReqs := append([]req(nil), reqs...)
	mu.Unlock()
	got := map[string][]string{}
	for _, r := range gotReqs {
		got[r.username] = append(got[r.username], r.method)
	}
	if len(got["alice"]) == 0 {
		t.Fatal("alice 没发出请求")
	}
	for _, u := range []string{"bob", "carol"} {
		if len(got[u]) == 0 {
			t.Fatalf("%s 没发出请求", u)
		}
	}

	// ---- -o json 模式:数组 + ok:false + error ----
	opts.output = outputJSON
	t.Cleanup(func() { opts.output = outputTable })
	out = captureStdout(t, func() {
		rootCmd.SetArgs([]string{"user", "rm", "alice", "bob", "carol"})
		_ = rootCmd.Execute()
	})
	var results []userBatchResult
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		t.Fatalf("-o json 输出的不是合法 JSON 数组: %v\n%s", err, out)
	}
	if len(results) != 3 {
		t.Fatalf("json 数组长度 = %d, want 3", len(results))
	}
	byName := map[string]userBatchResult{}
	for _, r := range results {
		byName[r.Username] = r
	}
	if !byName["alice"].OK {
		t.Fatalf("alice 的 ok = false, want true: %+v", byName["alice"])
	}
	for _, u := range []string{"bob", "carol"} {
		r := byName[u]
		if r.OK {
			t.Fatalf("%s 的 ok = true, want false", u)
		}
		if r.Error == "" {
			t.Fatalf("%s 的 error 是空的: %+v", u, r)
		}
	}
	// 文本模式输出里不能出现 json 数组;json 模式输出里不能有"完成:"。
	if strings.Contains(out, "完成:") {
		t.Fatalf("json 模式混进了文本汇总:\n%s", out)
	}
}

// TestBatchSingleNameKeepsShape 断言单名字时走原来的输出形状:
// 成功时是一句人话,不是逐行 + 汇总。
func TestBatchSingleNameKeepsShape(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}
	fakeControlPlane(t, handler)

	out := captureStdout(t, func() {
		rootCmd.SetArgs([]string{"user", "rm", "alice"})
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
	})
	if strings.Contains(out, "完成:") {
		t.Fatalf("单名字不该有汇总行:\n%s", out)
	}
	if strings.Contains(out, "alice:") {
		t.Fatalf("单名字不该有逐行前缀:\n%s", out)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatal("单名字成功时应当有一句输出")
	}
}

// TestBatchDedupeNamesRunsOnce 断言重复的名字只执行一次。
func TestBatchDedupeNamesRunsOnce(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	handler := func(w http.ResponseWriter, r *http.Request) {
		username := strings.TrimPrefix(r.URL.Path, "/api/users/")
		mu.Lock()
		calls = append(calls, username)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}
	fakeControlPlane(t, handler)

	out := captureStdout(t, func() {
		rootCmd.SetArgs([]string{"user", "rm", "alice", "bob", "alice"})
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
	})
	mu.Lock()
	callsCopy := append([]string(nil), calls...)
	mu.Unlock()
	if len(callsCopy) != 2 {
		t.Fatalf("请求数 = %d, want 2(重复名只执行一次): %v", len(callsCopy), callsCopy)
	}
	if callsCopy[0] != "alice" || callsCopy[1] != "bob" {
		t.Fatalf("执行顺序 = %v, want [alice bob](保序去重)", callsCopy)
	}
	// 输出行数与去重后的名字数一致,汇总也是 2 个成功。
	if !strings.Contains(out, "完成:2 个成功,0 个失败") {
		t.Fatalf("汇总行不对:\n%s", out)
	}
}

// TestBatchMultiLineMessage 断言多行返回体走 "== name ==" 块式展示,
// 这是 links 之类返回多行文本时的展示契约。
func TestBatchMultiLineMessage(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}
	fakeControlPlane(t, handler)

	// 直接调 runUserBatch,省掉一个真实命令;op 返回带换行的消息。
	out := captureStdout(t, func() {
		op := func(c *Client, username string) (string, []byte, error) {
			return fmt.Sprintf("第一行\n第二行"), []byte(`{"ok":true}`), nil
		}
		if err := runUserBatch([]string{"a", "b"}, op); err != nil {
			t.Fatalf("runUserBatch: %v", err)
		}
	})
	for _, want := range []string{"== a ==", "第一行", "第二行", "== b ==", "完成:2 个成功,0 个失败"} {
		if !strings.Contains(out, want) {
			t.Fatalf("输出里没有 %q:\n%s", want, out)
		}
	}
}
