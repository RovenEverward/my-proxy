package server

import (
	"bufio"
	"crypto/tls"
	"errors"
	"net"
	"sync"
	"time"
)

// 单端口同时说 HTTPS 与 HTTP:看连接的第一个字节。
//
// TLS 连接总是以一条握手记录开头,记录类型字节是 0x16;而任何 HTTP/1.x 请求都
// 以方法名(一个大写 ASCII 字母)开头。两者不会混淆,所以偷看一个字节就够了。
//
// 为什么不另开一个 HTTPS 端口:那要用户再去云厂商的安全组里放行一个口子,违背
// "一行命令装好"的初衷。订阅仍然走明文 HTTP —— 代理客户端普遍不认自签证书;
// 管理接口走 HTTPS,由 CLI 按证书指纹认人。
const tlsRecordHandshake = 0x16

// sniffTimeout 是等第一个字节的上限。偷看在各自的 goroutine 里做,慢客户端
// 不会挡住 Accept;但也不能让一个只连不发的连接永远占着 goroutine。
const sniffTimeout = 10 * time.Second

// sniffListener 包一个普通监听:Accept 出来的连接,TLS 的已被套上 *tls.Conn,
// 明文的原样(带上偷看过的那个字节)交出去。net/http 看到 *tls.Conn 会自己做
// 握手并填 r.TLS,所以下游 handler 用 r.TLS != nil 就能分辨来路。
type sniffListener struct {
	net.Listener
	tlsConfig *tls.Config

	conns chan net.Conn
	errc  chan error
	once  sync.Once
	done  chan struct{}
}

func newSniffListener(ln net.Listener, cfg *tls.Config) *sniffListener {
	l := &sniffListener{Listener: ln, tlsConfig: cfg,
		conns: make(chan net.Conn), errc: make(chan error, 1), done: make(chan struct{})}
	go l.acceptLoop()
	return l
}

func (l *sniffListener) acceptLoop() {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				time.Sleep(5 * time.Millisecond)
				continue
			}
			select {
			case l.errc <- err:
			case <-l.done:
			}
			return
		}
		go l.classify(c)
	}
}

func (l *sniffListener) classify(c net.Conn) {
	_ = c.SetReadDeadline(time.Now().Add(sniffTimeout))
	br := bufio.NewReaderSize(c, 1)
	b, err := br.Peek(1)
	_ = c.SetReadDeadline(time.Time{})
	if err != nil {
		c.Close()
		return
	}
	var out net.Conn = &peekedConn{Conn: c, r: br}
	if b[0] == tlsRecordHandshake {
		out = tls.Server(out, l.tlsConfig)
	}
	select {
	case l.conns <- out:
	case <-l.done:
		c.Close()
	}
}

func (l *sniffListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case err := <-l.errc:
		return nil, err
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *sniffListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return l.Listener.Close()
}

// peekedConn 让偷看过的字节重新出现在读流里。
type peekedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *peekedConn) Read(p []byte) (int, error) { return c.r.Read(p) }
