import type { Read } from "./types";

export class APIError extends Error {
  constructor(public status: number, message: string, public field?: string) { super(message); }
}
export const requestEvents = new EventTarget();
export let readEpoch = 0;
let batchDepth = 0;
const pendingWrites = new Set<string>();
function written(path: string) {
  if (batchDepth) pendingWrites.add(path);
  else requestEvents.dispatchEvent(new CustomEvent("written", { detail: path }));
}
export async function batchWrites<T>(operation: () => Promise<T>): Promise<T> {
  batchDepth++;
  try { return await operation(); }
  finally {
    batchDepth--;
    if (!batchDepth && pendingWrites.size) {
      const paths = [...pendingWrites]; pendingWrites.clear();
      requestEvents.dispatchEvent(new CustomEvent("written", { detail: paths }));
    }
  }
}
function reportFailure(path: string, status: number) { requestEvents.dispatchEvent(new CustomEvent("failure", { detail: { path, status } })); }
const isReadRequest = (path: string, init: RequestInit) => !init.method || init.method === "GET" || /\/proxy-groups\/preview(?:\?|$)|\/rules\/test$|\/settings\/test-webhook$|^\/local\/(profiles|setup\/check|relabel\/plan)$/.test(path);
export const sleep = (ms: number) => new Promise<void>(resolve => setTimeout(resolve, ms));
function invalidate(path: string) {
  readEpoch++;
  requestEvents.dispatchEvent(new CustomEvent("invalidate", { detail: path }));
}
export const api: Read = async <T,>(path: string, init: RequestInit & { asText?: boolean } = {}): Promise<T> => {
  const mutation = !isReadRequest(path, init);
  if (mutation) invalidate(path);
  for (let attempt = 0; ; attempt++) {
    let response: Response;
    try {
      response = await fetch(path, { credentials: "same-origin", ...init, headers: { Accept: "application/json", ...init.headers } });
    } catch { if (!init.signal?.aborted) reportFailure(path, 0); throw new APIError(0, "连不上本地服务：MPX 可能已经退出，请重新打开"); }
    if (mutation) invalidate(path);
    if (response.status === 429 && response.headers.get("Retry-After") && attempt < 3) {
      await sleep(1000 * (Number(response.headers.get("Retry-After")) || 2));
      continue;
    }
    const text = await response.text();
    let body: unknown = null;
    try { body = text ? JSON.parse(text) : null; } catch { body = text; }
    if (!response.ok) {
      reportFailure(path, response.status);
      const error = body && typeof body === "object" ? body as { error?: string; ui_hint?: string; field?: string } : null;
      throw new APIError(response.status, error?.ui_hint || error?.error || text || response.statusText, error?.field);
    }
    if (mutation) written(path);
    else requestEvents.dispatchEvent(new CustomEvent("response", { detail: { path, value: body } }));
    return (init.asText ? text : body) as T;
  }
};
export function write<T = unknown>(method: string, path: string, body?: unknown): Promise<T> {
  return api<T>(path, { method, headers: body === undefined ? {} : { "Content-Type": "application/json" }, body: body === undefined ? undefined : JSON.stringify(body) });
}
