// MPX CDN 优选测速端点(UC-31):返回定长随机字节流。
// 探测端对它做限时下载测速 —— IP 直拨 + Host/SNI 是 worker 域名,
// 速度反映「探测点→该 IP 落地的边缘→回传」的真实路径吞吐。
// 用法:GET /?bytes=<1..104857600> ,默认 25MB。带 CORS 无妨,纯测速。
export default {
  async fetch(request) {
    const u = new URL(request.url);
    let bytes = parseInt(u.searchParams.get("bytes") || "", 10);
    if (!Number.isFinite(bytes) || bytes < 1) bytes = 25 * 1024 * 1024;
    if (bytes > 100 * 1024 * 1024) bytes = 100 * 1024 * 1024;

    const CHUNK = 1024 * 1024; // 1 MiB 一块,流式出
    const chunk = new Uint8Array(CHUNK);
    for (let i = 0; i < CHUNK; i++) chunk[i] = i & 0xff;

    const stream = new ReadableStream({
      start(controller) {
        let left = bytes;
        const push = () => {
          while (left > 0) {
            const n = left > CHUNK ? CHUNK : left;
            controller.enqueue(n === CHUNK ? chunk : chunk.subarray(0, n));
            left -= n;
          }
          controller.close();
        };
        push();
      },
    });
    return new Response(stream, {
      headers: {
        "Content-Type": "application/octet-stream",
        "Content-Length": String(bytes),
        "Cache-Control": "no-store",
      },
    });
  },
};
