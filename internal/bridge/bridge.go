// Package bridge is a loopback HTTP proxy that forwards to an upstream
// proxy with credentials. Chromium's --proxy-server cannot carry a username
// and password, so facet points it at the bridge instead.
//
// CONNECT tunnels are dialed through the upstream (HTTP CONNECT with
// Proxy-Authorization, or SOCKS5 with username/password); plain HTTP
// requests go through an http.Transport using the same upstream. Target
// hostnames are passed to the upstream unresolved, so DNS happens at the
// proxy's exit, not on this machine.
package bridge

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"
)

const dialTimeout = 20 * time.Second

// Bridge is a running loopback proxy.
type Bridge struct {
	listener net.Listener
	server   *http.Server
	dial     func(ctx context.Context, addr string) (net.Conn, error)
	upstream *url.URL
	client   *http.Transport
	logger   *log.Logger

	mu      sync.Mutex
	tunnels map[net.Conn]struct{}
}

// Start listens on 127.0.0.1 with an OS-chosen port and serves until
// Close. upstream is http, https, socks5 or socks5h, with optional userinfo.
func Start(upstream *url.URL, logger *log.Logger) (*Bridge, error) {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	b := &Bridge{upstream: upstream, logger: logger, tunnels: map[net.Conn]struct{}{}}
	switch upstream.Scheme {
	case "http", "https":
		b.dial = b.dialConnect
		b.client = &http.Transport{Proxy: http.ProxyURL(upstream)}
	case "socks5", "socks5h":
		var auth *proxy.Auth
		if upstream.User != nil {
			pw, _ := upstream.User.Password()
			auth = &proxy.Auth{User: upstream.User.Username(), Password: pw}
		}
		d, err := proxy.SOCKS5("tcp", upstream.Host, auth, &net.Dialer{Timeout: dialTimeout})
		if err != nil {
			return nil, err
		}
		cd := d.(proxy.ContextDialer)
		b.dial = func(ctx context.Context, addr string) (net.Conn, error) { return cd.DialContext(ctx, "tcp", addr) }
		b.client = &http.Transport{DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			return b.dial(ctx, addr)
		}}
	default:
		return nil, fmt.Errorf("bridge: unsupported upstream scheme %q", upstream.Scheme)
	}
	b.client.ForceAttemptHTTP2 = false
	b.client.MaxIdleConnsPerHost = 4
	b.client.IdleConnTimeout = 90 * time.Second

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	b.listener = ln
	b.server = &http.Server{
		Handler:           b,
		ReadHeaderTimeout: 30 * time.Second,
		ErrorLog:          logger,
	}
	go func() {
		if err := b.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Printf("bridge: serve: %v", err)
		}
	}()
	return b, nil
}

// URL is what Chromium's --proxy-server should point at.
func (b *Bridge) URL() string { return "http://" + b.listener.Addr().String() }

// Close stops accepting and drops open tunnels.
func (b *Bridge) Close() error {
	err := b.server.Close()
	b.mu.Lock()
	for c := range b.tunnels {
		c.Close()
	}
	b.mu.Unlock()
	b.client.CloseIdleConnections()
	return err
}

func (b *Bridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		b.serveConnect(w, r)
		return
	}
	if r.URL.Scheme != "http" || r.URL.Host == "" {
		http.Error(w, "facet bridge: only proxy requests are served", http.StatusBadRequest)
		return
	}
	out := r.Clone(r.Context())
	out.RequestURI = ""
	removeHopHeaders(out.Header)
	resp, err := b.client.RoundTrip(out)
	if err != nil {
		b.logger.Printf("bridge: %s %s: %v", r.Method, r.URL.Host, err)
		http.Error(w, "facet bridge: upstream: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusProxyAuthRequired {
		// Passed through, a 407 would make Chromium prompt for credentials
		// the bridge exists to supply.
		b.logger.Printf("bridge: %s %s: upstream rejected the proxy credentials (407)", r.Method, r.URL.Host)
		http.Error(w, "facet bridge: upstream rejected the proxy credentials", http.StatusBadGateway)
		return
	}
	removeHopHeaders(resp.Header)
	for k, vs := range resp.Header {
		w.Header()[k] = vs
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (b *Bridge) serveConnect(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), dialTimeout)
	target, err := b.dial(ctx, r.Host)
	cancel()
	if err != nil {
		b.logger.Printf("bridge: CONNECT %s: %v", r.Host, err)
		http.Error(w, "facet bridge: upstream: "+err.Error(), http.StatusBadGateway)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		target.Close()
		http.Error(w, "facet bridge: hijack unsupported", http.StatusInternalServerError)
		return
	}
	client, buf, err := hj.Hijack()
	if err != nil {
		target.Close()
		return
	}
	// The server's header timeout is still armed on the hijacked conn.
	_ = client.SetDeadline(time.Time{})
	if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		client.Close()
		target.Close()
		return
	}
	b.track(client, target)
	defer b.untrack(client, target)

	done := make(chan struct{}, 2)
	go func() {
		// buf.Reader first yields whatever the client sent after the CONNECT head.
		_, _ = io.Copy(target, buf.Reader)
		closeWrite(target)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, target)
		closeWrite(client)
		done <- struct{}{}
	}()
	<-done
	<-done
	client.Close()
	target.Close()
}

// dialConnect opens a tunnel to addr through an HTTP(S) upstream.
func (b *Bridge) dialConnect(ctx context.Context, addr string) (net.Conn, error) {
	d := &net.Dialer{Timeout: dialTimeout}
	conn, err := d.DialContext(ctx, "tcp", b.upstream.Host)
	if err != nil {
		return nil, err
	}
	if b.upstream.Scheme == "https" {
		tc := tls.Client(conn, &tls.Config{ServerName: b.upstream.Hostname()})
		if err := tc.HandshakeContext(ctx); err != nil {
			conn.Close()
			return nil, err
		}
		conn = tc
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	req := "CONNECT " + addr + " HTTP/1.1\r\nHost: " + addr + "\r\n"
	if u := b.upstream.User; u != nil {
		pw, _ := u.Password()
		req += "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(u.Username()+":"+pw)) + "\r\n"
	}
	if _, err := io.WriteString(conn, req+"\r\n"); err != nil {
		conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		conn.Close()
		return nil, err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		conn.Close()
		if resp.StatusCode == http.StatusProxyAuthRequired {
			return nil, errors.New("upstream rejected the proxy credentials (407)")
		}
		return nil, fmt.Errorf("upstream CONNECT: %s", resp.Status)
	}
	_ = conn.SetDeadline(time.Time{})
	if br.Buffered() > 0 {
		return &bufferedConn{Conn: conn, r: br}, nil
	}
	return conn, nil
}

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

func (c *bufferedConn) CloseWrite() error {
	closeWrite(c.Conn)
	return nil
}

func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
		return
	}
	c.Close()
}

func (b *Bridge) track(cs ...net.Conn) {
	b.mu.Lock()
	for _, c := range cs {
		b.tunnels[c] = struct{}{}
	}
	b.mu.Unlock()
}

func (b *Bridge) untrack(cs ...net.Conn) {
	b.mu.Lock()
	for _, c := range cs {
		delete(b.tunnels, c)
	}
	b.mu.Unlock()
}

var hopHeaders = []string{
	"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate",
	"Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
}

func removeHopHeaders(h http.Header) {
	for _, f := range h["Connection"] {
		for _, name := range strings.Split(f, ",") {
			h.Del(strings.TrimSpace(name))
		}
	}
	for _, name := range hopHeaders {
		h.Del(name)
	}
}
