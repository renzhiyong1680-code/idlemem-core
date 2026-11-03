package heartbeat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// HTTPReporter 通过 HTTP POST + JSON 上报心跳的参考实现。
// 生产环境可用 gRPC/WebSocket 替换，接口保持不变。
type HTTPReporter struct {
	endpoint string
	client   *http.Client
	timeout  time.Duration
	headers  map[string]string
}

// HTTPOption 配置 HTTPReporter。
type HTTPOption func(*HTTPReporter)

// WithTimeout 设置单次上报超时。
func WithTimeout(d time.Duration) HTTPOption {
	return func(r *HTTPReporter) { r.timeout = d }
}

// WithHTTPClient 注入自定义 http.Client。
func WithHTTPClient(c *http.Client) HTTPOption {
	return func(r *HTTPReporter) { r.client = c }
}

// WithHeader 附加自定义请求头（如鉴权 Token）。
func WithHeader(k, v string) HTTPOption {
	return func(r *HTTPReporter) {
		if r.headers == nil {
			r.headers = make(map[string]string)
		}
		r.headers[k] = v
	}
}

// NewHTTPReporter 创建 HTTP 心跳上报器。
func NewHTTPReporter(endpoint string, opts ...HTTPOption) *HTTPReporter {
	r := &HTTPReporter{
		endpoint: endpoint,
		timeout:  5 * time.Second,
	}
	for _, o := range opts {
		o(r)
	}
	if r.client == nil {
		r.client = &http.Client{Timeout: r.timeout}
	}
	return r
}

// Report 序列化快照并 POST 到控制面端点。
func (r *HTTPReporter) Report(ctx context.Context, snap Snapshot) error {
	body, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("heartbeat: marshal snapshot: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("heartbeat: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range r.headers {
		req.Header.Set(k, v)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("heartbeat: send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("heartbeat: unexpected status %s", resp.Status)
	}
	return nil
}

// Close 释放空闲连接。
func (r *HTTPReporter) Close() error {
	if tr, ok := r.client.Transport.(*http.Transport); ok {
		tr.CloseIdleConnections()
	}
	return nil
}
