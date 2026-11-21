import { write } from "./api";

// 管理台的会话入口(UC-26):登录拿 cookie,登出删服务端会话再整页重载。
// 两处都走 reload 而不是本地 state:cookie 是 HttpOnly,JS 拿不到也改不了,
// 重载让浏览器与服务端会话状态重新对齐,最不容易残留旧数据。

export async function login(username: string, password: string): Promise<void> {
  await write("POST", "/api/admin/login", { username, password });
}

export async function logout(): Promise<void> {
  // 过期或已吊销的会话也要能体面退出:logout 接口不要凭据,失败也照常重载。
  try { await write("POST", "/api/admin/logout", {}); } catch { /* 就当已经退了 */ }
  location.hash = "login";
  location.reload();
}
