// Command xiancun-agent 是闲存云 Agent 的可运行外壳。
//
// 它把 idlemem-core 核心库装配成一个真实运行的"闲置内存贡献节点"：
//   - 周期性采集本机内存、计算可贡献容量、心跳上报控制面；
//   - 暴露一个最小 Redis 协议端口，供企业客户端或控制面网关存取缓存；
//   - 全程零第三方依赖（仅标准库），可在 Linux/macOS/Windows 上编译运行。
//
// 用法：
//
//	xiancun-agent -server http://127.0.0.1:8080 -kv :16380
//	xiancun-agent -config agent.json
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/xiancuncloud/idlemem-core/agent"
	"github.com/xiancuncloud/idlemem-core/governor"
	"github.com/xiancuncloud/idlemem-core/heartbeat"
	"github.com/xiancuncloud/idlemem-core/kvstore"
	"github.com/xiancuncloud/idlemem-core/meminfo"
	"github.com/xiancuncloud/idlemem-core/nodeid"
)

// defaultKVAddr 默认 KV 服务监听地址（与生产网关端口区分开）。
const defaultKVAddr = ":16380"

// config Agent 运行配置，可通过 -config <json> 文件或命令行 flag 提供。
type config struct {
	// Server 控制面 REST API 地址，如 http://127.0.0.1:8080。
	Server string `json:"server"`
	// KVAddr KV 服务监听地址。
	KVAddr string `json:"kv_addr"`
	// NodeID 节点 ID；为空时自动生成稳定 ID。
	NodeID string `json:"node_id"`
	// Interval 心跳上报间隔（纳秒）。
	Interval time.Duration `json:"interval"`
	// KVMaxBytes KV 引擎容量上限（0 表示由治理器动态决定）。
	KVMaxBytes uint64 `json:"kv_max_bytes"`
}

func (c *config) applyDefaults() {
	if c.Server == "" {
		c.Server = "http://127.0.0.1:8080"
	}
	if c.KVAddr == "" {
		c.KVAddr = defaultKVAddr
	}
	if c.Interval <= 0 {
		c.Interval = 10 * time.Second
	}
}

func main() {
	logger := log.New(os.Stdout, "[agent] ", log.LstdFlags|log.Lmsgprefix)

	var (
		cfgPath = flag.String("config", "", "config json file path (optional)")
		server  = flag.String("server", "", "control plane API address, e.g. http://127.0.0.1:8080")
		kvAddr  = flag.String("kv", "", "KV service listen address, e.g. :16380")
		nodeID  = flag.String("id", "", "node id (default: auto-generated stable id)")
		interval = flag.Duration("interval", 0, "heartbeat interval (default 10s)")
	)
	flag.Parse()

	cfg := config{}
	// 优先从 JSON 文件加载，flag 覆盖同名项。
	if *cfgPath != "" {
		if err := loadConfig(*cfgPath, &cfg); err != nil {
			logger.Fatalf("load config: %v", err)
		}
	}
	if *server != "" {
		cfg.Server = *server
	}
	if *kvAddr != "" {
		cfg.KVAddr = *kvAddr
	}
	if *nodeID != "" {
		cfg.NodeID = *nodeID
	}
	if *interval > 0 {
		cfg.Interval = *interval
	}
	cfg.applyDefaults()

	// 节点 ID：未指定则自动生成稳定 ID。
	id := cfg.NodeID
	if id == "" {
		id = nodeid.String()
	}
	logger.Printf("node id: %s", id)

	// 装配核心库。
	prov := meminfo.SystemProvider{}
	rep := heartbeat.NewHTTPReporter(cfg.Server + "/api/v1/nodes/heartbeat")
	core, err := agent.New(agent.Config{
		NodeID:         id,
		Version:        agent.Version,
		MemProvider:    prov,
		GovernorCfg:    governor.DefaultConfig(),
		Reporter:       rep,
		StoreOpts:      kvstore.Options{MaxBytes: cfg.KVMaxBytes},
		ReportInterval: cfg.Interval,
	})
	if err != nil {
		logger.Fatalf("create agent core: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 启动 KV 服务（阻塞在 goroutine）。
	kvSrv, err := newKVServe(cfg.KVAddr, core, logger)
	if err != nil {
		logger.Fatalf("create kv service: %v", err)
	}
	go kvSrv.serve()

	// 启动采样与心跳循环。
	if err := core.Start(ctx); err != nil {
		logger.Fatalf("start agent: %v", err)
	}
	logger.Printf("agent started: server=%s kv=%s interval=%s", cfg.Server, cfg.KVAddr, cfg.Interval)

	<-ctx.Done()
	logger.Printf("shutting down...")
	kvSrv.close()
	core.Stop()
}

// ---------- 最小 RESP 服务器（零第三方依赖） ----------

// kvSrv 在 Agent 内嵌 KV 引擎上提供最小 Redis 协议服务。
type kvSrv struct {
	addr   string
	ln     net.Listener
	core   *agent.Agent
	logger *log.Logger
	wg     sync.WaitGroup
}

func newKVServe(addr string, core *agent.Agent, logger *log.Logger) (*kvSrv, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	return &kvSrv{addr: addr, ln: ln, core: core, logger: logger}, nil
}

func (s *kvSrv) serve() {
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

func (s *kvSrv) close() {
	_ = s.ln.Close()
	s.wg.Wait()
}

func (s *kvSrv) handleConn(conn net.Conn) {
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

func (s *kvSrv) handleSet(conn net.Conn, args []string) {
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
	st := s.core.Store()
	// 容量治理：写入后总占用不得超过引擎上限与当前可贡献容量。
	need := uint64(len(value))
	after := st.UsedBytes() + need
	cap := s.core.Capacity()
	if !st.CanFit(need) || (cap > 0 && after > cap) {
		writeError(conn, "ERR out of capacity")
		return
	}
	st.Set(key, value, ttl)
	s.syncUsed()
	writeSimple(conn, "OK")
}

func (s *kvSrv) handleGet(conn net.Conn, args []string) {
	if len(args) < 2 {
		writeError(conn, "ERR wrong number of arguments for 'get' command")
		return
	}
	v, ok := s.core.Store().Get(args[1])
	if !ok {
		writeNull(conn)
		return
	}
	writeBulk(conn, v)
}

func (s *kvSrv) handleDel(conn net.Conn, args []string) {
	if len(args) < 2 {
		writeError(conn, "ERR wrong number of arguments for 'del' command")
		return
	}
	n := 0
	for _, k := range args[1:] {
		if s.core.Store().Delete(k) {
			n++
		}
	}
	s.syncUsed()
	writeInt(conn, n)
}

func (s *kvSrv) handleExpire(conn net.Conn, args []string) {
	if len(args) < 3 {
		writeError(conn, "ERR wrong number of arguments for 'expire' command")
		return
	}
	sec, err := parseSeconds(args[2])
	if err != nil {
		writeError(conn, "ERR value is not an integer or out of range")
		return
	}
	st := s.core.Store()
	v, ok := st.Get(args[1])
	if !ok {
		writeInt(conn, 0)
		return
	}
	st.Set(args[1], v, sec)
	writeInt(conn, 1)
}

func (s *kvSrv) handleTTL(conn net.Conn, args []string) {
	if len(args) < 2 {
		writeError(conn, "ERR wrong number of arguments for 'ttl' command")
		return
	}
	d, ok := s.core.Store().TTL(args[1])
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

func (s *kvSrv) handleInfo(conn net.Conn) {
	st := s.core.Store()
	info := fmt.Sprintf("idlemem-agent\r\nkeys:%d\r\nused_bytes:%d\r\ncapacity_bytes:%d\r\nstatus:%s\r\n",
		st.Len(), st.UsedBytes(), s.core.Capacity(), s.core.Status())
	writeBulk(conn, []byte(info))
}

// syncUsed 把 KV 引擎占用同步给 Agent，随下次心跳上报。
func (s *kvSrv) syncUsed() {
	s.core.SetUsedBytes(s.core.Store().UsedBytes())
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
