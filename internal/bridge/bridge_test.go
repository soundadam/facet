package bridge

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

const testUser, testPass = "alice", "s3cr:t@pw"

// targets serves "plain" over HTTP and "tls" over HTTPS.
func targets(t *testing.T) (plain, secure *httptest.Server) {
	t.Helper()
	plain = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" {
			http.Error(w, "proxy credentials leaked to the origin", http.StatusTeapot)
			return
		}
		io.WriteString(w, "plain")
	}))
	secure = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "tls")
	}))
	t.Cleanup(plain.Close)
	t.Cleanup(secure.Close)
	return plain, secure
}

// clientVia talks to the bridge the way Chromium does: absolute-URI
// requests for http, CONNECT for https.
func clientVia(t *testing.T, b *Bridge, secure *httptest.Server) *http.Client {
	t.Helper()
	pu, err := url.Parse(b.URL())
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(secure.Certificate())
	tr := &http.Transport{
		Proxy: http.ProxyURL(pu),
		// The httptest certificate names example.com, not localhost.
		TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: "example.com"},
	}
	t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr}
}

func get(t *testing.T, c *http.Client, rawURL string) (int, string, error) {
	t.Helper()
	resp, err := c.Get(rawURL)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body), err
}

// localhostURL swaps 127.0.0.1 for localhost so the hostname, not an
// address, has to reach the upstream.
func localhostURL(s *httptest.Server) string {
	return strings.Replace(s.URL, "127.0.0.1", "localhost", 1)
}

func startBridge(t *testing.T, upstream *url.URL) *Bridge {
	t.Helper()
	b, err := Start(upstream, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return b
}

// httpProxy is an upstream HTTP proxy that requires Basic credentials.
type httpProxy struct {
	*httptest.Server
	hits atomic.Int32
}

func startHTTPProxy(t *testing.T) *httpProxy {
	t.Helper()
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(testUser+":"+testPass))
	p := &httpProxy{}
	direct := &http.Transport{}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.hits.Add(1)
		if r.Header.Get("Proxy-Authorization") != want {
			w.Header().Set("Proxy-Authenticate", `Basic realm="test"`)
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		if r.Method == http.MethodConnect {
			target, err := net.Dial("tcp", r.Host)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				target.Close()
				return
			}
			io.WriteString(conn, "HTTP/1.1 200 OK\r\n\r\n")
			splice(conn, target)
			return
		}
		out := r.Clone(r.Context())
		out.RequestURI = ""
		out.Header.Del("Proxy-Authorization")
		resp, err := direct.RoundTrip(out)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}))
	t.Cleanup(p.Close)
	t.Cleanup(direct.CloseIdleConnections)
	return p
}

func (p *httpProxy) url(user, pass string) *url.URL {
	u, _ := url.Parse(p.URL)
	u.User = url.UserPassword(user, pass)
	return u
}

func splice(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { io.Copy(a, b); a.Close(); wg.Done() }()
	go func() { io.Copy(b, a); b.Close(); wg.Done() }()
	wg.Wait()
}

// socksServer is a minimal RFC 1928 server with RFC 1929 credentials.
type socksServer struct {
	ln net.Listener
	mu sync.Mutex
	// requested holds CONNECT targets as the client named them.
	requested []string
}

func startSOCKS(t *testing.T) *socksServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &socksServer{ln: ln}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handle(c)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return s
}

func (s *socksServer) url(user, pass string) *url.URL {
	return &url.URL{Scheme: "socks5", Host: s.ln.Addr().String(), User: url.UserPassword(user, pass)}
}

func (s *socksServer) handle(c net.Conn) {
	fail := func() { c.Close() }
	buf := make([]byte, 262)
	if _, err := io.ReadFull(c, buf[:2]); err != nil || buf[0] != 5 {
		fail()
		return
	}
	methods := buf[2 : 2+int(buf[1])]
	if _, err := io.ReadFull(c, methods); err != nil || !bytes.Contains(methods, []byte{2}) {
		c.Write([]byte{5, 0xff})
		fail()
		return
	}
	c.Write([]byte{5, 2})
	readField := func() (string, error) {
		if _, err := io.ReadFull(c, buf[:1]); err != nil {
			return "", err
		}
		f := buf[1 : 1+int(buf[0])]
		_, err := io.ReadFull(c, f)
		return string(f), err
	}
	if _, err := io.ReadFull(c, buf[:1]); err != nil || buf[0] != 1 {
		fail()
		return
	}
	user, err1 := readField()
	pass, err2 := readField()
	if err1 != nil || err2 != nil || user != testUser || pass != testPass {
		c.Write([]byte{1, 1})
		fail()
		return
	}
	c.Write([]byte{1, 0})
	if _, err := io.ReadFull(c, buf[:4]); err != nil || buf[1] != 1 {
		fail()
		return
	}
	var host string
	switch buf[3] {
	case 1:
		io.ReadFull(c, buf[:4])
		host = net.IP(buf[:4]).String()
	case 4:
		io.ReadFull(c, buf[:16])
		host = net.IP(buf[:16]).String()
	case 3:
		name, err := readField()
		if err != nil {
			fail()
			return
		}
		host = name
	default:
		fail()
		return
	}
	if _, err := io.ReadFull(c, buf[:2]); err != nil {
		fail()
		return
	}
	addr := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(buf[:2]))))
	s.mu.Lock()
	s.requested = append(s.requested, addr)
	s.mu.Unlock()
	target, err := net.Dial("tcp", addr)
	if err != nil {
		c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		fail()
		return
	}
	c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	splice(c, target)
}

func (s *socksServer) targets() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requested...)
}

func TestHTTPUpstream(t *testing.T) {
	plain, secure := targets(t)
	up := startHTTPProxy(t)
	b := startBridge(t, up.url(testUser, testPass))
	c := clientVia(t, b, secure)

	if code, body, err := get(t, c, plain.URL+"/x"); err != nil || code != 200 || body != "plain" {
		t.Fatalf("plain: %d %q %v", code, body, err)
	}
	if code, body, err := get(t, c, localhostURL(secure)+"/x"); err != nil || code != 200 || body != "tls" {
		t.Fatalf("tunnel: %d %q %v", code, body, err)
	}
	if up.hits.Load() < 2 {
		t.Fatalf("upstream saw %d requests", up.hits.Load())
	}
}

func TestHTTPUpstreamBadCredentials(t *testing.T) {
	plain, secure := targets(t)
	up := startHTTPProxy(t)
	b := startBridge(t, up.url(testUser, "wrong"))
	c := clientVia(t, b, secure)

	// A 407 must not reach Chromium, or it would prompt for credentials.
	if code, _, err := get(t, c, plain.URL); err != nil || code != http.StatusBadGateway {
		t.Fatalf("plain: %d %v; want 502", code, err)
	}
	if _, _, err := get(t, c, secure.URL); err == nil || !strings.Contains(err.Error(), "Bad Gateway") {
		t.Fatalf("tunnel err %v; want Bad Gateway", err)
	}
}

func TestSOCKS5Upstream(t *testing.T) {
	plain, secure := targets(t)
	s := startSOCKS(t)
	b := startBridge(t, s.url(testUser, testPass))
	c := clientVia(t, b, secure)

	if code, body, err := get(t, c, localhostURL(plain)); err != nil || code != 200 || body != "plain" {
		t.Fatalf("plain: %d %q %v", code, body, err)
	}
	if code, body, err := get(t, c, localhostURL(secure)); err != nil || code != 200 || body != "tls" {
		t.Fatalf("tunnel: %d %q %v", code, body, err)
	}
	// Hostnames reach the proxy unresolved: DNS happens at the exit.
	got := s.targets()
	if len(got) != 2 || !strings.HasPrefix(got[0], "localhost:") || !strings.HasPrefix(got[1], "localhost:") {
		t.Fatalf("socks targets %v; want localhost names", got)
	}
}

func TestSOCKS5BadCredentials(t *testing.T) {
	_, secure := targets(t)
	s := startSOCKS(t)
	b := startBridge(t, s.url(testUser, "wrong"))
	c := clientVia(t, b, secure)
	if _, _, err := get(t, c, secure.URL); err == nil {
		t.Fatal("tunnel opened with wrong socks credentials")
	}
	if got := s.targets(); len(got) != 0 {
		t.Fatalf("socks server connected %v after failed auth", got)
	}
}

func TestRejectsUnknownScheme(t *testing.T) {
	if _, err := Start(&url.URL{Scheme: "ftp", Host: "127.0.0.1:21"}, nil); err == nil {
		t.Fatal("ftp upstream accepted")
	}
}

func TestOriginRequestsRefused(t *testing.T) {
	up := startHTTPProxy(t)
	b := startBridge(t, up.url(testUser, testPass))
	resp, err := http.Get(b.URL() + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d; want 400", resp.StatusCode)
	}
}
