package caddytrafficmirror

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"go.uber.org/zap"
)

func init() {
	caddy.RegisterModule(TrafficMirror{})
	httpcaddyfile.RegisterHandlerDirective("traffic_mirror", parseCaddyfileHandlerDirective)
}

// TrafficMirror 实现流量复制中间件
type TrafficMirror struct {
	// 目标URL，流量将复制到这个地址
	TargetURL string `json:"target_url,omitempty"`

	// 要匹配的路由路径前缀
	Routes []string `json:"routes,omitempty"`

	// 要匹配的HTTP方法
	Methods []string `json:"methods,omitempty"`

	// 复制请求的超时时间（秒）
	Timeout int `json:"timeout,omitempty"`

	// 是否在请求头中包含镜像标识
	AddMirrorHeaders bool `json:"add_mirror_headers,omitempty"`

	// 镜像请求日志级别 (debug, info, warn, error)
	LogLevel string `json:"log_level,omitempty"`

	// 是否禁用镜像请求日志
	DisableLog bool `json:"disable_log,omitempty"`

	logger     *zap.Logger
	client     *http.Client
	bufferPool *sync.Pool
	routeSet   map[string]struct{}
	methodSet  map[string]struct{}
}

// CaddyModule 返回模块信息
func (TrafficMirror) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.traffic_mirror",
		New: func() caddy.Module { return new(TrafficMirror) },
	}
}

// Provision 模块初始化
func (m *TrafficMirror) Provision(ctx caddy.Context) error {
	m.logger = ctx.Logger()

	// 设置默认值
	if m.Timeout == 0 {
		m.Timeout = 30
	}
	if m.LogLevel == "" {
		m.LogLevel = "debug"
	}

	// 初始化用于复用请求体缓冲区的池
	m.bufferPool = &sync.Pool{
		New: func() interface{} {
			return new(bytes.Buffer)
		},
	}

	// 将路由和方法切片转换为 map 以提高查找效率
	if len(m.Routes) > 0 {
		m.routeSet = make(map[string]struct{}, len(m.Routes))
		for _, route := range m.Routes {
			m.routeSet[route] = struct{}{}
		}
	}
	if len(m.Methods) > 0 {
		m.methodSet = make(map[string]struct{}, len(m.Methods))
		for _, method := range m.Methods {
			m.methodSet[strings.ToUpper(method)] = struct{}{}
		}
	}

	// 创建自定义 transport 以优化连接池
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          200, // 增加最大空闲连接数
		MaxIdleConnsPerHost:   100, // 增加每个主机的最大空闲连接数
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	// 创建HTTP客户端
	m.client = &http.Client{
		Transport: transport,
		Timeout:   time.Duration(m.Timeout) * time.Second,
		// 不跟随重定向
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	m.logger.Info("traffic mirror module provisioned",
		zap.String("target_url", m.TargetURL),
		zap.Int("timeout", m.Timeout),
		zap.String("log_level", m.LogLevel),
		zap.Bool("disable_log", m.DisableLog))

	return nil
}

// Validate 验证配置
func (m *TrafficMirror) Validate() error {
	if m.TargetURL == "" {
		return fmt.Errorf("target_url is required")
	}
	if m.Timeout <= 0 {
		return fmt.Errorf("timeout must be greater than 0")
	}

	// 验证URL格式
	if !strings.HasPrefix(m.TargetURL, "http://") && !strings.HasPrefix(m.TargetURL, "https://") {
		return fmt.Errorf("target_url must start with http:// or https://")
	}

	// 验证日志级别
	if !m.DisableLog {
		switch strings.ToLower(m.LogLevel) {
		case "debug", "info", "warn", "error":
			// 合法级别
		default:
			return fmt.Errorf("invalid log_level: %s, must be one of debug, info, warn, error", m.LogLevel)
		}
	}

	return nil
}

// copyRequestBody 安全地复制请求体，并使用 sync.Pool 优化内存分配
func (m *TrafficMirror) copyRequestBody(r *http.Request) (*bytes.Buffer, error) {
	if r.Body == nil || r.Body == http.NoBody {
		return nil, nil
	}

	// 从池中获取一个缓冲区
	buf := m.bufferPool.Get().(*bytes.Buffer)
	buf.Reset() // 确保缓冲区是空的

	// 将请求体同时写入缓冲区和丢弃目标，以恢复原始请求体
	// 这样后续的处理程序仍然可以读取它
	r.Body = io.NopCloser(io.TeeReader(r.Body, buf))

	// 必须读取整个请求体，以确保缓冲区被完全填充
	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		m.bufferPool.Put(buf) // 出错时将缓冲区放回池中
		return nil, fmt.Errorf("failed to copy request body: %w", err)
	}

	// 再次恢复请求体，因为上面的 io.Copy 消耗了它
	r.Body = io.NopCloser(bytes.NewReader(buf.Bytes()))

	return buf, nil
}

// shouldMirror 检查是否应该复制这个请求
func (m *TrafficMirror) shouldMirror(r *http.Request) bool {
	// 使用 map 检查路由匹配，提高效率
	if len(m.routeSet) > 0 {
		pathMatched := false
		// 虽然这里仍然是循环，但它避免了对原始切片的迭代
		// 对于大量前缀，更高级的结构（如Trie）会更快
		for route := range m.routeSet {
			if strings.HasPrefix(r.URL.Path, route) {
				pathMatched = true
				break
			}
		}
		if !pathMatched {
			return false
		}
	}

	// 使用 map 检查方法匹配，实现 O(1) 查找
	if len(m.methodSet) > 0 {
		if _, ok := m.methodSet[r.Method]; !ok {
			return false
		}
	}

	return true
}

// createMirrorRequest 创建复制请求
func (m *TrafficMirror) createMirrorRequest(originalReq *http.Request, body *bytes.Buffer) (*http.Request, error) {
	// 构建目标URL
	targetURL := m.TargetURL + originalReq.URL.Path
	if originalReq.URL.RawQuery != "" {
		targetURL += "?" + originalReq.URL.RawQuery
	}

	var bodyReader io.Reader
	if body != nil {
		bodyReader = body
	}

	// 创建新请求
	mirrorReq, err := http.NewRequest(originalReq.Method, targetURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create mirror request: %w", err)
	}

	// 复制头部
	mirrorReq.Header = originalReq.Header.Clone()

	// 移除不应复制的头部
	mirrorReq.Header.Del("Content-Length")
	mirrorReq.Header.Del("Connection")
	mirrorReq.Header.Del("Keep-Alive")

	// 添加标识头
	if m.AddMirrorHeaders {
		mirrorReq.Header.Set("X-Traffic-Mirror", "true")
		mirrorReq.Header.Set("X-Original-Host", originalReq.Host)
	}

	// 设置内容长度
	if body != nil {
		mirrorReq.ContentLength = int64(body.Len())
	}

	return mirrorReq, nil
}

// sendMirrorRequest 发送复制请求
func (m *TrafficMirror) sendMirrorRequest(mirrorReq *http.Request, body *bytes.Buffer) {
	// 使用完后将缓冲区放回池中
	if body != nil {
		defer m.bufferPool.Put(body)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(m.Timeout)*time.Second)
	defer cancel()

	mirrorReq = mirrorReq.WithContext(ctx)

	start := time.Now()
	resp, err := m.client.Do(mirrorReq)
	duration := time.Since(start)

	// 如果禁用了日志，则直接返回
	if m.DisableLog {
		if err == nil && resp.Body != nil {
			// 仍然需要消费并关闭响应体
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		return
	}

	if err != nil {
		// 失败日志统一使用 debug 级别，避免网络抖动等情况污染 error 日志
		m.logger.Debug("mirror request failed",
			zap.String("url", mirrorReq.URL.String()),
			zap.String("method", mirrorReq.Method),
			zap.Duration("duration", duration),
			zap.Error(err))
		return
	}

	// 重要：读取并关闭响应体，避免资源泄露
	if resp.Body != nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}

	fields := []zap.Field{
		zap.String("url", mirrorReq.URL.String()),
		zap.String("method", mirrorReq.Method),
		zap.Int("status", resp.StatusCode),
		zap.Duration("duration", duration),
	}

	switch m.LogLevel {
	case "info":
		m.logger.Info("mirror request completed", fields...)
	case "warn":
		m.logger.Warn("mirror request completed", fields...)
	case "error":
		m.logger.Error("mirror request completed", fields...)
	default: // "debug"
		m.logger.Debug("mirror request completed", fields...)
	}
}

// ServeHTTP 处理HTTP请求
func (m *TrafficMirror) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	// 检查是否需要复制这个请求
	if !m.shouldMirror(r) {
		return next.ServeHTTP(w, r)
	}

	// 复制请求体（这会恢复原始请求体）
	bodyBuffer, err := m.copyRequestBody(r)
	if err != nil {
		m.logger.Error("failed to copy request body", zap.Error(err))
		// 即使复制失败，也继续处理原始请求
		return next.ServeHTTP(w, r)
	}

	// 创建复制请求
	mirrorReq, err := m.createMirrorRequest(r, bodyBuffer)
	if err != nil {
		m.logger.Error("failed to create mirror request", zap.Error(err))
		if bodyBuffer != nil {
			m.bufferPool.Put(bodyBuffer) // 确保缓冲区被归还
		}
		return next.ServeHTTP(w, r)
	}

	// 异步发送复制请求
	go m.sendMirrorRequest(mirrorReq, bodyBuffer)

	// 继续处理原始请求
	return next.ServeHTTP(w, r)
}

// UnmarshalCaddyfile 解析Caddyfile配置
func (m *TrafficMirror) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	// 默认值
	m.AddMirrorHeaders = true

	for d.Next() {
		// 解析参数
		for d.NextBlock(0) {
			switch d.Val() {
			case "target_url":
				if !d.NextArg() {
					return d.ArgErr()
				}
				m.TargetURL = d.Val()

			case "routes":
				m.Routes = d.RemainingArgs()
				if len(m.Routes) == 0 {
					return d.ArgErr()
				}

			case "methods":
				m.Methods = d.RemainingArgs()
				if len(m.Methods) == 0 {
					return d.ArgErr()
				}

			case "timeout":
				if !d.NextArg() {
					return d.ArgErr()
				}
				var timeout int
				if _, err := fmt.Sscanf(d.Val(), "%d", &timeout); err != nil {
					return d.Errf("invalid timeout: %v", err)
				}
				m.Timeout = timeout

			case "add_mirror_headers":
				if d.NextArg() {
					switch d.Val() {
					case "true", "yes", "on":
						m.AddMirrorHeaders = true
					case "false", "no", "off":
						m.AddMirrorHeaders = false
					default:
						return d.Errf("invalid add_mirror_headers value: %s", d.Val())
					}
				} else {
					m.AddMirrorHeaders = true
				}

			case "log_level":
				if !d.NextArg() {
					return d.ArgErr()
				}
				m.LogLevel = strings.ToLower(d.Val())

			case "disable_log":
				if d.NextArg() {
					switch d.Val() {
					case "true", "yes", "on":
						m.DisableLog = true
					case "false", "no", "off":
						m.DisableLog = false
					default:
						return d.Errf("invalid disable_log value: %s", d.Val())
					}
				} else {
					m.DisableLog = true // 如果只写 disable_log，默认为 true
				}

			default:
				return d.Errf("unrecognized subdirective: %s", d.Val())
			}
		}
	}
	return nil
}

// parseCaddyfileHandlerDirective 解析 Caddyfile 中的 traffic_mirror 指令
func parseCaddyfileHandlerDirective(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	var m TrafficMirror
	err := m.UnmarshalCaddyfile(h.Dispenser)
	return &m, err
}

// Interface guards
var (
	_ caddy.Provisioner           = (*TrafficMirror)(nil)
	_ caddy.Validator             = (*TrafficMirror)(nil)
	_ caddyhttp.MiddlewareHandler = (*TrafficMirror)(nil)
	_ caddyfile.Unmarshaler       = (*TrafficMirror)(nil)
)
