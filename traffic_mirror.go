package caddytrafficmirror

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
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

	logger *zap.Logger
	client *http.Client
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

	// 创建HTTP客户端
	m.client = &http.Client{
		Timeout: time.Duration(m.Timeout) * time.Second,
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

// copyRequestBody 安全地复制请求体
func (m *TrafficMirror) copyRequestBody(r *http.Request) ([]byte, error) {
	if r.Body == nil || r.Body == http.NoBody {
		return nil, nil
	}

	// 读取原始请求体
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read request body: %w", err)
	}

	// 恢复原始请求体，以便后续处理
	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	return bodyBytes, nil
}

// shouldMirror 检查是否应该复制这个请求
func (m *TrafficMirror) shouldMirror(r *http.Request) bool {
	// 检查路由匹配
	if len(m.Routes) > 0 {
		pathMatched := false
		for _, route := range m.Routes {
			if strings.HasPrefix(r.URL.Path, route) {
				pathMatched = true
				break
			}
		}
		if !pathMatched {
			return false
		}
	}

	// 检查方法匹配
	if len(m.Methods) > 0 {
		methodMatched := false
		for _, method := range m.Methods {
			if r.Method == method {
				methodMatched = true
				break
			}
		}
		if !methodMatched {
			return false
		}
	}

	return true
}

// createMirrorRequest 创建复制请求
func (m *TrafficMirror) createMirrorRequest(originalReq *http.Request, body []byte) (*http.Request, error) {
	// 构建目标URL
	targetURL := m.TargetURL + originalReq.URL.Path
	if originalReq.URL.RawQuery != "" {
		targetURL += "?" + originalReq.URL.RawQuery
	}

	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewBuffer(body)
	}

	// 创建新请求
	mirrorReq, err := http.NewRequest(originalReq.Method, targetURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create mirror request: %w", err)
	}

	// 复制头部
	for key, values := range originalReq.Header {
		// 跳过一些不应该复制的头部
		lowerKey := strings.ToLower(key)
		if lowerKey == "content-length" || lowerKey == "connection" || lowerKey == "keep-alive" {
			continue
		}
		for _, value := range values {
			mirrorReq.Header.Add(key, value)
		}
	}

	// 添加标识头
	if m.AddMirrorHeaders {
		mirrorReq.Header.Set("X-Traffic-Mirror", "true")
		mirrorReq.Header.Set("X-Original-Host", originalReq.Host)
	}

	// 设置内容长度
	if body != nil {
		mirrorReq.ContentLength = int64(len(body))
	}

	return mirrorReq, nil
}

// sendMirrorRequest 发送复制请求
func (m *TrafficMirror) sendMirrorRequest(mirrorReq *http.Request) {
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
	bodyBytes, err := m.copyRequestBody(r)
	if err != nil {
		m.logger.Error("failed to copy request body", zap.Error(err))
		// 即使复制失败，也继续处理原始请求
		return next.ServeHTTP(w, r)
	}

	// 创建复制请求
	mirrorReq, err := m.createMirrorRequest(r, bodyBytes)
	if err != nil {
		m.logger.Error("failed to create mirror request", zap.Error(err))
		return next.ServeHTTP(w, r)
	}

	// 异步发送复制请求
	go m.sendMirrorRequest(mirrorReq)

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
