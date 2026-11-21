import { useState, type FormEvent } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Field, Icon } from "@/components/primitives";
import { login } from "@/runtime/auth";
import type { PageProps } from "@/runtime/types";
import { APIError } from "@/runtime/api";

// 登录页(UC-26):账号 + 密码 → POST /api/admin/login 种会话 cookie。
// 全屏独立页,不走 AppShell 侧栏;登录成功由 App 重新取 meta 后进总览。
// 后端统一回「用户名或密码不对」,前端不猜是哪一项错,只做两件事:
// 输入为空时本地拦,服务端错误原样展示(含锁定提示)。
export function LoginPage(_: PageProps) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (busy) return;
    if (!username.trim() || !password) { setError("用户名和密码都要填"); return; }
    setBusy(true);
    setError("");
    try {
      await login(username.trim(), password);
      // cookie 已种下:重载整页,让 App 重新取 /api/admin/meta 进工作区。
      location.hash = "";
      location.reload();
    } catch (cause) {
      setBusy(false);
      setPassword("");
      if (cause instanceof APIError && cause.status === 429) setError(cause.message || "尝试太频繁，请稍后再试");
      else if (cause instanceof APIError) setError(cause.message);
      else setError("连不上控制面，请稍后再试");
    }
  };
  return <div className="login"><div className="lg-card" role="main">
    <div className="lg-brand"><img className="lk lk-l" src="lockup-light.svg" alt="MPX" /><img className="lk lk-d" src="lockup-dark.svg" alt="MPX" /></div>
    <h1 className="lg-title">登录管理台</h1>
    <p className="sub lg-sub">用管理员账号登录这台控制面</p>
    <form onSubmit={event => void submit(event)} noValidate>
      <Field label="用户名" htmlFor="lg-user">
        <Input id="lg-user" autoComplete="username" autoFocus value={username} disabled={busy}
          onChange={event => setUsername(event.target.value)} />
      </Field>
      <Field label="密码" htmlFor="lg-pass">
        <Input id="lg-pass" type="password" autoComplete="current-password" value={password} disabled={busy}
          onChange={event => setPassword(event.target.value)} />
      </Field>
      {error && <div className="err lg-err" role="alert" id="lg-err"><Icon name="warn" />{error}</div>}
      <Button variant="primary" className="lg-btn" type="submit" loading={busy} data-act="login-submit">
        <Icon name="inb" />登录
      </Button>
    </form>
  </div></div>;
}
