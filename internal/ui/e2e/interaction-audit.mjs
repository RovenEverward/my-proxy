// 诊断模式复用现有端到端路径；不记录输入值、响应正文、查询参数或订阅链接。
import { appendFileSync, mkdirSync } from "node:fs";
import path from "node:path";
const installed = new WeakSet();
export async function auditInteractions(page) {
  if (installed.has(page)) return;
  installed.add(page);
  const out = process.env.MPX_INTERACTION_AUDIT;
  mkdirSync(path.dirname(out), { recursive: true });
  await page.exposeBinding("recordInteraction", (_, record) => appendFileSync(out, JSON.stringify(record) + "\n"));
  await page.addInitScript(() => {
    const records = [];
    let active = null, sequence = 0, requestSequence = 0;
    const visible = el => el instanceof Element && !!el.getClientRects().length && getComputedStyle(el).visibility !== "hidden";
    const emit = record => {
      record.final = Math.max(record.lastVisible || 0, ...record.requests.map(r => r.end || r.start), 0);
      window.recordInteraction({ ...record, requests: record.requests.map(r => ({ ...r })) }).catch(() => {});
    };
    const original = window.fetch;
    window.fetch = async function(input, init) {
      const url = new URL(typeof input === "string" ? input : input.url, location.href);
      if (!url.pathname.startsWith("/api/")) return original.call(this, input, init);
      const owner = active;
      const start = performance.now();
      const delay = 150 + (++requestSequence % 4) * 50;
      let signature = 2166136261;
      for (const char of url.search + "\n" + (init?.body || "")) signature = Math.imul(signature ^ char.charCodeAt(0), 16777619) >>> 0;
      const req = { method: init?.method || "GET", path: url.pathname, signature: signature.toString(16), delay, start: owner ? start - owner.at : 0 };
      if (owner) { owner.requests.push(req); emit(owner); }
      try {
        const response = await original.call(this, input, init);
        await new Promise(resolve => setTimeout(resolve, delay));
        req.status = response.status;
        return response;
      } finally {
        req.duration = performance.now() - start;
        req.end = owner ? performance.now() - owner.at : req.duration;
        if (owner) emit(owner);
      }
    };
    const begin = event => {
      const el = event.target.closest?.('button,a,select,input,textarea,summary,[data-act],[role="button"],th');
      if (!el || !visible(el)) return;
      if (active && active.element === el && event.type === "change" && performance.now() - active.at < 40) {
        active.feedback ??= 0; emit(active); return;
      }
      if (active) { active.overlapped = active.requests.some(r => !r.end); emit(active); }
      const label = el.dataset.act || el.id || el.getAttribute("aria-label") || (el.matches('a') ? el.hash : el.textContent.trim().slice(0, 32)) || el.tagName;
      active = { id: `${performance.timeOrigin}:${++sequence}`, page: location.hash.split('/')[0], event: event.type,
        control: label, tag: el.tagName, at: performance.now(), feedback: null, lastVisible: 0, requests: [] };
      Object.defineProperty(active, "element", { value: el });
      if (event.type === "change" || event.type === "input") active.feedback = 0;
      records.push(active); emit(active);
    };
    document.addEventListener("click", begin, true);
    document.addEventListener("change", begin, true);
    document.addEventListener("input", begin, true);
    document.addEventListener("DOMContentLoaded", () => {
      new MutationObserver(changes => {
        if (!active) return;
        const changed = changes.some(m => {
          const el = m.target.nodeType === Node.ELEMENT_NODE ? m.target : m.target.parentElement;
          if (!el || el.closest('.toast') && m.type === 'attributes' || el === document.documentElement) return false;
          if (m.type === 'attributes' && !['class','disabled','checked','aria-busy','aria-expanded','open','style'].includes(m.attributeName)) return false;
          if (m.type === 'childList') return [...m.addedNodes].some(n => visible(n.nodeType === Node.ELEMENT_NODE ? n : n.parentElement));
          return visible(el);
        });
        if (changed) {
          const elapsed = performance.now() - active.at;
          active.feedback ??= elapsed;
          active.lastVisible = elapsed;
          emit(active);
        }
      }).observe(document.body, { subtree: true, childList: true, attributes: true, characterData: true });
    });
    addEventListener('pagehide', () => records.forEach(emit));
  });
}
