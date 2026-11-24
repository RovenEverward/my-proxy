package server

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// marzbanMint 按 Marzban 的铸法造一枚测试 token,与 app/utils/jwt.py 对齐。
func marzbanMint(username, secret string) string {
	data := base64.URLEncoding.EncodeToString([]byte(username + ",1795504056"))
	sum := sha256.Sum256([]byte(data + secret))
	return data + base64.URLEncoding.EncodeToString(sum[:])[:10]
}

// saveSettings 直接改库里的设置,免得每回都走一遍整份 PUT。
func (e *env) saveSettings(mutate func(*model.Settings)) {
	e.t.Helper()
	st, err := e.st.GetSettings()
	if err != nil {
		e.t.Fatalf("读设置: %v", err)
	}
	mutate(st)
	if err := e.st.SaveSettings(st); err != nil {
		e.t.Fatalf("写设置: %v", err)
	}
}

// TestMarzbanTokenFallback 迁库场景:用户的 sub_token 是 MPX 自己那串,客户端
// 手里却是 Marzban 时代发出去的 token —— 用同一个 secret 验签后必须还能认
// 到这个人。
func TestMarzbanTokenFallback(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("hk", "node.example.com")
	in := e.createInbound(n.ID, "vless-ws", 8443)
	e.createHost(in.ID, "香港", "node.example.com")
	user := e.createUser("alice")

	const secret = "marzban-jwt-secret-32-bytesxxxx"
	e.saveSettings(func(st *model.Settings) { st.MarzbanSubSecret = secret })

	legacy := marzbanMint("alice", secret)

	t.Run("旧 token 拿到同一个用户的订阅", func(t *testing.T) {
		code, _, body := e.sub("/sub/"+legacy+"/links", "v2rayng")
		if code != http.StatusOK {
			t.Fatalf("旧 token 应当 200,实际 %d %s", code, body)
		}
		if !strings.Contains(string(body), "vless://") {
			t.Fatalf("正文里没有链接:\n%s", body)
		}
	})

	t.Run("签名不符的旧 token 不认", func(t *testing.T) {
		bad := legacy[:len(legacy)-10] + "AAAAAAAAAA"
		if code, _, _ := e.sub("/sub/"+bad+"/links", "v2rayng"); code != http.StatusNotFound {
			t.Fatalf("改过的 token 应当 404,实际 %d", code)
		}
	})

	t.Run("secret 没配时旧 token 不认", func(t *testing.T) {
		e.saveSettings(func(st *model.Settings) { st.MarzbanSubSecret = "" })
		if code, _, _ := e.sub("/sub/"+legacy+"/links", "v2rayng"); code != http.StatusNotFound {
			t.Fatalf("没配 secret 应当 404,实际 %d", code)
		}
		e.saveSettings(func(st *model.Settings) { st.MarzbanSubSecret = secret })
	})

	t.Run("MPX 自己的 token 不受 fallback 影响", func(t *testing.T) {
		code, _, body := e.sub("/sub/"+user.SubToken+"/links", "v2rayng")
		if code != http.StatusOK {
			t.Fatalf("正常 token 应当 200,实际 %d %s", code, body)
		}
	})

	t.Run("别的用户的旧 token 不串号", func(t *testing.T) {
		e.createUser("bob")
		code, _, body := e.sub("/sub/"+marzbanMint("bob", secret)+"/links", "v2rayng")
		if code != http.StatusOK {
			t.Fatalf("bob 的旧 token 应当 200,实际 %d %s", code, body)
		}
		if !strings.Contains(string(body), "vless://") {
			t.Fatalf("bob 的正文里没有链接:\n%s", body)
		}
	})
}

// TestCreateUserWithExplicitSubToken 建用户时显式给 sub_token,服务端要采纳
// 而不是换成自动生成的 —— 迁库要把外部 token 原样带过来。
func TestCreateUserWithExplicitSubToken(t *testing.T) {
	e := newEnv(t)

	code, data := e.api("POST", "/api/users", model.User{
		Username: "carol", Enabled: true, SubToken: "marzban-legacy-token-1",
	})
	if code != http.StatusCreated {
		t.Fatalf("建用户: got %d %s", code, data)
	}
	got := decodeJSON[userView](e.t, data)
	if got.SubToken != "marzban-legacy-token-1" {
		t.Fatalf("SubToken = %q, 想要 marzban-legacy-token-1", got.SubToken)
	}

	// 用它当路径 token 拉订阅:这证明落库的就是它,不只是回显。
	code, _, body := e.sub("/sub/marzban-legacy-token-1/links", "v2rayng")
	if code != http.StatusOK {
		t.Fatalf("显式 token 拉订阅: got %d %s", code, body)
	}

	// 非法字符的 token 不被采纳,仍回退自动生成。
	code, data = e.api("POST", "/api/users", model.User{
		Username: "dave", Enabled: true, SubToken: "bad token/with:chars",
	})
	if code != http.StatusCreated {
		t.Fatalf("带非法 token 建用户: got %d %s", code, data)
	}
	got = decodeJSON[userView](e.t, data)
	if got.SubToken == "bad token/with:chars" || got.SubToken == "" {
		t.Fatalf("非法 token 不应被采纳: %q", got.SubToken)
	}
}
