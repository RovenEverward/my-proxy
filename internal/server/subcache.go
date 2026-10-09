package server

import (
	"bytes"
	"compress/gzip"
	"sync"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// 订阅缓存:同一个用户的同一格式,在没有管理动作时返回字节级一致的副本。
//
// 客户端刷新订阅是最频繁的请求,而每次都要 ListNodes + ListInbounds + ListHosts +
// 渲染一整份配置。这台机器上的用户量不大,但每次刷新都重算一份,既浪费也让
// sqlite 的连接竞争变多;同一份内容在两次刷新之间基本不变,所以把它缓存下来。
//
// 缓存的正确性靠两点:1) 键是 (subToken, format, pathFormat),不同用户、不同
// 格式互不干扰;2) 任何一个管理接口改动落库后由 invalidateSubCache 全清,缓存
// 不会留着过期的入口。两者都便宜:全清是一行 map 删除,代价只是下一次刷新重算。
//
// 缓存的是 body + 头(客户端认的那些头都在这里),ETag/If-None-Match 在 body 上
// 做 —— 头本身是确定性的,不需要缓存也能重现。

type subCacheKey struct {
	subToken   string
	format     model.ClientFormat
	pathFormat string
}

type subCacheEntry struct {
	body     []byte // 可能已 gzip
	etag     string // sha256 hex of the *uncompressed* body
	encoding string // "" 或 "gzip"
	user     *model.User
	settings *model.Settings
	headers  map[string]string // 客户端认的那几个,见 setSubHeaders
}

type subCache struct {
	mu    sync.RWMutex
	items map[subCacheKey]*subCacheEntry
}

func (c *subCache) get(k subCacheKey) *subCacheEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.items[k]
}

func (c *subCache) put(k subCacheKey, e *subCacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		c.items = make(map[subCacheKey]*subCacheEntry)
	}
	c.items[k] = e
}

// invalidate 全清。调用时机是任何一个管理接口写库成功后 —— 内容变了,下一次
// 刷新该拿到新的,而不是把差一点点的旧内容发出去。
//
// 只有写路径主动调用。订阅缓存的淘汰不抄读写放大的招(按 user/inbound 算依赖再
// 定点失效),因为业务上"哪些影响哪些"的关系写一次就要维护一次,改错了一处就
// 会漏一条 stale;写侧频率低,全清代价可以忽略。
func (c *subCache) invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = nil
}

// invalidateSubCache 是写路径末尾的统一钩子。调用方把它放在「写库成功」之后、
// 「返回响应」之前;后续新加的任何写库动作都要过它,不然订阅会晚一拍生效。
func (s *Server) invalidateSubCache() { s.subCache.invalidate() }

// gzipBody 把 body 压成 gzip。
func gzipBody(body []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(body); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
