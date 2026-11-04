// Package kvsrv 在核心库 KV 引擎上提供最小 Redis 协议服务（零第三方依赖）。
//
// 它把 KV 引擎暴露为一个标准 RESP 端口，供企业客户端或控制面网关以 Redis
// 协议存取缓存。支持 PING / QUIT / SET [EX] / GET / DEL / EXPIRE / TTL / INFO。
// Agent 外壳（CLI、Windows 托盘、macOS 菜单栏）共用本组件。
package kvsrv

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/xiancuncloud/idlemem-core/heartbeat"
	"github.com/xiancuncloud/idlemem-core/kvstore"
)

// Backend 是 RESP 服务器依赖的 KV 后端（由 Agent 装配实现）。
type Backend interface {
	// Store 返回内嵌 KV 引擎。
	Store() *kvstore.Store
	// Capacity 返回当前可贡献容量（治理器动态决定）。
	Capacity() uint64
	// Status 返回节点状态。
	Status() heartbeat.Status
	// SetUsedBytes 同步 KV 引擎占用，随下次心跳上报控制面。
	SetUsedBytes(n uint64)
}

// Server 一个 RESP KV 服务实例。
type Server struct {
	addr    string
	ln      net.Listener
	backend Backend
	logger  *log.Logger
	wg      sync.WaitGroup
}

// New 监听 addr 并返回 Server（尚未开始 accept）。
func New(addr string, backend Backend, logger *log.Logger) (*Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	return &Server{addr: addr, ln: ln, backend: backend, logger: logger}, nil
}

// Serve 阻塞接受连接并处理，直到 Close 被调用。
func (s *Server) Serve() {
	s.logger.Printf("kv: listening on %s (redis protocol)", s.addr)
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleConn(conn)
		}()
	}
}

// Close 停止监听并等待已处理连接结束。
func (s *Server) Close() {
	_ = s.ln.Close()
	s.wg.Wait()
}

// Addr 返回实际监听地址（含系统分配的端口）。
func (s *Server) Addr() string { return s.ln.Addr().String() }

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()
	r := newRESPReader(conn)
	for {
		args, err := r.readCommand()
		if err != nil {
			return
		}
		if len(args) == 0 {
			return
		}
		verb := toUpper(args[0])
		switch verb {
		case "PING":
			writeSimple(conn, "PONG")
		case "QUIT":
			writeSimple(conn, "OK")
			return
		case "SET":
			s.handleSet(conn, args)
		case "GET":
			s.handleGet(conn, args)
		case "DEL":
			s.handleDel(conn, args)
		case "EXPIRE":
			s.handleExpire(conn, args)
		case "TTL":
			s.handleTTL(conn, args)
		case "INFO":
			s.handleInfo(conn)
		default:
			writeError(conn, "ERR unsupported command: "+args[0])
		}
	}
}

func (s *Server) handleSet(conn net.Conn, args []string) {
	if len(args) < 3 {
		writeError(conn, "ERR wrong number of arguments for 'set' command")
		return
	}
	key, value := args[1], []byte(args[2])
	var ttl time.Duration
	if len(args) >= 5 && toUpper(args[3]) == "EX" {
		sec, err := parseSeconds(args[4])
		if err != nil {
			writeError(conn, "ERR invalid expire time in 'set' command")
			return
		}
		ttl = sec
	}
	st := s.backend.Store()
	// 容量治理：写入后总占用不得超过引擎上限与当前可贡献容量。
	need := uint64(len(value))
	after := st.UsedBytes() + need
	cap := s.backend.Capacity()
	if !st.CanFit(need) || (cap > 0 && after > cap) {
		writeError(conn, "ERR out of capacity")
		return
	}
	st.Set(key, value, ttl)
	s.syncUsed()
	writeSimple(conn, "OK")
}

func (s *Server) handleGet(conn net.Conn, args []string) {
	if len(args) < 2 {
		writeError(conn, "ERR wrong number of arguments for 'get' command")
		return
	}
	v, ok := s.backend.Store().Get(args[1])
	if !ok {
		writeNull(conn)
		return
	}
	writeBulk(conn, v)
}

func (s *Server) handleDel(conn net.Conn, args []string) {
	if len(args) < 2 {
		writeError(conn, "ERR wrong number of arguments for 'del' command")
		return
	}
	n := 0
	for _, k := range args[1:] {
		if s.backend.Store().Delete(k) {
			n++
		}
	}
	s.syncUsed()
	writeInt(conn, n)
}

func (s *Server) handleExpire(conn net.Conn, args []string) {
	if len(args) < 3 {
		writeError(conn, "ERR wrong number of arguments for 'expire' command")
		return
	}
	sec, err := parseSeconds(args[2])
	if err != nil {
		writeError(conn, "ERR value is not an integer or out of range")
		return
	}
	st := s.backend.Store()
	v, ok := st.Get(args[1])
	if !ok {
		writeInt(conn, 0)
		return
	}
	st.Set(args[1], v, sec)
	writeInt(conn, 1)
}

func (s *Server) handleTTL(conn net.Conn, args []string) {
	if len(args) < 2 {
		writeError(conn, "ERR wrong number of arguments for 'ttl' command")
		return
	}
	d, ok := s.backend.Store().TTL(args[1])
	if !ok {
		writeInt(conn, -2)
		return
	}
	if d < 0 {
		writeInt(conn, -1)
		return
	}
	writeInt(conn, int(d.Seconds()))
}

func (s *Server) handleInfo(conn net.Conn) {
	st := s.backend.Store()
	info := fmt.Sprintf("idlemem-agent\r\nkeys:%d\r\nused_bytes:%d\r\ncapacity_bytes:%d\r\nstatus:%s\r\n",
		st.Len(), st.UsedBytes(), s.backend.Capacity(), s.backend.Status())
	writeBulk(conn, []byte(info))
}

// syncUsed 把 KV 引擎占用同步给后端，随下次心跳上报。
func (s *Server) syncUsed() {
	s.backend.SetUsedBytes(s.backend.Store().UsedBytes())
}

// ---------- RESP 编解码（最小实现） ----------

type respReader struct {
	conn net.Conn
	buf  [4096]byte
	pos  int
	n    int
}

func newRESPReader(conn net.Conn) *respReader { return &respReader{conn: conn} }

func (r *respReader) readCommand() ([]string, error) {
	line, err := r.readLine()
	if err != nil {
		return nil, err
	}
	if len(line) == 0 || line[0] != '*' {
		return nil, fmt.Errorf("bad command line")
	}
	var n int
	if _, err := fmt.Sscanf(string(line[1:]), "%d", &n); err != nil || n <= 0 {
		return nil, fmt.Errorf("bad array length")
	}
	args := make([]string, 0, n)
	for i := 0; i < n; i++ {
		hdr, err := r.readLine()
		if err != nil {
			return nil, err
		}
		if len(hdr) == 0 || hdr[0] != '$' {
			return nil, fmt.Errorf("bad bulk header")
		}
		var blen int
		if _, err := fmt.Sscanf(string(hdr[1:]), "%d", &blen); err != nil || blen < 0 {
			return nil, fmt.Errorf("bad bulk length")
		}
		body := make([]byte, blen)
		if _, err := readFull(r, body); err != nil {
			return nil, err
		}
		if _, err := r.readLine(); err != nil { // 消费尾部 CRLF
			return nil, err
		}
		args = append(args, string(body))
	}
	return args, nil
}

func (r *respReader) readLine() ([]byte, error) {
	for {
		// 在已缓冲数据中找 \r\n。
		for i := r.pos; i+1 < r.n; i++ {
			if r.buf[i] == '\r' && r.buf[i+1] == '\n' {
				line := make([]byte, i-r.pos)
				copy(line, r.buf[r.pos:i])
				r.pos = i + 2
				return line, nil
			}
		}
		if r.pos == r.n {
			r.pos, r.n = 0, 0
		} else if r.pos > 0 {
			// 压缩缓冲区。
			copy(r.buf[:], r.buf[r.pos:r.n])
			r.n -= r.pos
			r.pos = 0
		}
		n, err := r.conn.Read(r.buf[r.n:])
		if err != nil {
			return nil, err
		}
		r.n += n
		if r.n >= len(r.buf) {
			return nil, fmt.Errorf("line too long")
		}
	}
}

func readFull(r *respReader, dst []byte) (int, error) {
	total := 0
	for total < len(dst) {
		if r.pos < r.n {
			c := copy(dst[total:], r.buf[r.pos:r.n])
			r.pos += c
			total += c
			continue
		}
		r.pos, r.n = 0, 0
		n, err := r.conn.Read(r.buf[:])
		if err != nil {
			return total, err
		}
		r.n = n
	}
	return total, nil
}

func writeSimple(conn net.Conn, s string) {
	_, _ = conn.Write([]byte("+" + s + "\r\n"))
}

func writeError(conn net.Conn, s string) {
	_, _ = conn.Write([]byte("-" + s + "\r\n"))
}

func writeInt(conn net.Conn, n int) {
	_, _ = conn.Write([]byte(fmt.Sprintf(":%d\r\n", n)))
}

func writeBulk(conn net.Conn, b []byte) {
	_, _ = conn.Write([]byte(fmt.Sprintf("$%d\r\n", len(b))))
	_, _ = conn.Write(b)
	_, _ = conn.Write([]byte("\r\n"))
}

func writeNull(conn net.Conn) {
	_, _ = conn.Write([]byte("$-1\r\n"))
}

func parseSeconds(s string) (time.Duration, error) {
	var sec int64
	if _, err := fmt.Sscanf(s, "%d", &sec); err != nil || sec <= 0 {
		return 0, fmt.Errorf("invalid seconds")
	}
	return time.Duration(sec) * time.Second, nil
}

func toUpper(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'a' && b[i] <= 'z' {
			b[i] -= 'a' - 'A'
		}
	}
	return string(b)
}
