package server

import "testing"

func TestTokenEqual(t *testing.T) {
	if !tokenEqual("abc", "abc") {
		t.Fatal("相同 token 应相等")
	}
	if tokenEqual("abc", "abcd") {
		t.Fatal("不同长度不应相等")
	}
	if tokenEqual("abc", "xyz") {
		t.Fatal("不同内容不应相等")
	}
	if tokenEqual("", "x") || tokenEqual("x", "") {
		t.Fatal("空与非空不应相等")
	}
}
