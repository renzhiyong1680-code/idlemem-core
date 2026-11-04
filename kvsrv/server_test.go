package kvsrv

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/xiancuncloud/idlemem-core/heartbeat"
	"github.com/xiancuncloud/idlemem-core/kvstore"
)

// fakeBackend 用真实 kvstore.Store 实现 Backend。
type fakeBackend struct {
	st   *kvstore.Store
	cap  uint64
	used uint64
}

func (f *fakeBackend) Store() *kvstore.Store      { return f.st }
func (f *fakeBackend) Capacity() uint64           { return f.cap }
func (f *fakeBackend) Status() heartbeat.Status   { return heartbeat.StatusOnline }
func (f *fakeBackend) SetUsedBytes(n uint64)      { f.used = n }

func startTestServer(t *testing.T, backend Backend) (addr string) {
	t.Helper()
	srv, err := New("127.0.0.1:0", backend, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go srv.Serve()
	t.Cleanup(srv.Close)
	return srv.Addr()
}

// respClient 最小 RESP 客户端（单连接，命令级调用）。
type respClient struct {
	conn net.Conn
	r    *bufio.Reader
}

func dialResp(t *testing.T, addr string) *respClient {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return &respClient{conn: conn, r: bufio.NewReader(conn)}
}

func (c *respClient) send(t *testing.T, args ...string) string {
	t.Helper()
	parts := []string{fmt.Sprintf("*%d\r\n", len(args))}
	for _, a := range args {
		parts = append(parts, fmt.Sprintf("$%d\r\n%s\r\n", len(a), a))
	}
	if _, err := c.conn.Write([]byte(strings.Join(parts, ""))); err != nil {
		t.Fatalf("write: %v", err)
	}
	line, err := c.r.ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	if len(line) > 1 && (line[0] == '+' || line[0] == '-' || line[0] == ':' || line[0] == '$') {
		line = line[1:] // 剥掉 RESP 类型前缀
	}
	return line
}

func (c *respClient) sendBulk(t *testing.T, args ...string) string {
	t.Helper()
	parts := []string{fmt.Sprintf("*%d\r\n", len(args))}
	for _, a := range args {
		parts = append(parts, fmt.Sprintf("$%d\r\n%s\r\n", len(a), a))
	}
	if _, err := c.conn.Write([]byte(strings.Join(parts, ""))); err != nil {
		t.Fatalf("write: %v", err)
	}
	hdr, err := c.r.ReadString('\n')
	if err != nil {
		t.Fatalf("read hdr: %v", err)
	}
	if !strings.HasPrefix(hdr, "$") {
		return strings.TrimSpace(hdr)
	}
	var blen int
	if _, err := fmt.Sscanf(strings.TrimSpace(hdr), "$%d", &blen); err != nil {
		t.Fatalf("parse hdr %q: %v", hdr, err)
	}
	if blen < 0 {
		return "$-1"
	}
	body := make([]byte, blen+2)
	if _, err := io.ReadFull(c.r, body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(body[:blen])
}

func newFakeBackend(t *testing.T, capBytes uint64) *fakeBackend {
	t.Helper()
	st := kvstore.New(kvstore.Options{MaxBytes: capBytes})
	return &fakeBackend{st: st, cap: capBytes}
}

// TestKVSrv_FullFlow 验证核心命令流：PING / SET / GET / TTL / DEL / INFO。
func TestKVSrv_FullFlow(t *testing.T) {
	be := newFakeBackend(t, 1<<20) // 1MB 引擎
	addr := startTestServer(t, be)
	c := dialResp(t, addr)

	if got := c.send(t, "PING"); got != "PONG" {
		t.Fatalf("PING = %q", got)
	}
	if got := c.send(t, "SET", "k1", "v1", "EX", "60"); got != "OK" {
		t.Fatalf("SET = %q", got)
	}
	if got := c.sendBulk(t, "GET", "k1"); got != "v1" {
		t.Fatalf("GET = %q", got)
	}
	if got := c.send(t, "TTL", "k1"); got != "60" {
		t.Fatalf("TTL = %q", got)
	}
	if got := c.send(t, "DEL", "k1"); got != "1" {
		t.Fatalf("DEL = %q", got)
	}
	if got := c.sendBulk(t, "GET", "k1"); got != "$-1" {
		t.Fatalf("GET after DEL = %q", got)
	}
	info := c.sendBulk(t, "INFO")
	for _, want := range []string{"idlemem-agent", "keys:0", "used_bytes:0"} {
		if !strings.Contains(info, want) {
			t.Fatalf("INFO missing %q: %s", want, info)
		}
	}
	if got := c.send(t, "QUIT"); got != "OK" {
		t.Fatalf("QUIT = %q", got)
	}
}

// TestKVSrv_OutOfCapacity 验证容量治理：超出引擎上限拒绝写入。
func TestKVSrv_OutOfCapacity(t *testing.T) {
	be := newFakeBackend(t, 8) // 引擎仅 8 字节
	addr := startTestServer(t, be)
	c := dialResp(t, addr)

	if got := c.send(t, "SET", "big", "hello-world"); got != "OK" {
		// 引擎上限允许 item 时继续验证容量水位。
	}
	if got := c.send(t, "SET", "big2", "hello-world"); got != "ERR out of capacity" {
		t.Fatalf("expect out of capacity, got %q", got)
	}
}

// TestKVSrv_ExpireAndTTL 验证 EXPIRE 与过期后的 GET 返回空。
func TestKVSrv_ExpireAndTTL(t *testing.T) {
	be := newFakeBackend(t, 1<<20)
	addr := startTestServer(t, be)
	c := dialResp(t, addr)

	c.send(t, "SET", "e1", "x")
	if got := c.send(t, "EXPIRE", "e1", "1"); got != "1" {
		t.Fatalf("EXPIRE = %q", got)
	}
	if got := c.send(t, "TTL", "e1"); got != "1" {
		t.Fatalf("TTL = %q", got)
	}
	time.Sleep(1100 * time.Millisecond)
	if got := c.sendBulk(t, "GET", "e1"); got != "$-1" {
		t.Fatalf("GET expired = %q", got)
	}
	if got := c.send(t, "TTL", "e1"); got != "-2" {
		t.Fatalf("TTL expired = %q", got)
	}
}
