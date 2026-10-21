package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// idlePollInterval 是强制关闭阶段等待在途请求退出的轮询间隔。
const idlePollInterval = time.Millisecond

// Deps 是 HTTP 层的外部依赖：会话生命周期与 Run 执行能力都由装配层注入。
type Deps struct {
	// Sessions 提供会话创建与预检读取，必填。
	Sessions SessionStore
	// Runner 执行一次 Agent Run，必填。
	Runner Runner
}

// Server 是 HTTP 对外协议层的可复用构造结果。
//
// 生命周期：New（只校验配置，不监听）→ Serve（监听并提供服务）→ Shutdown/Close。
// 关闭后不再接受新连接；宽限期满时取消服务根 context，使在途 Run 的 ctx 收到取消。
type Server struct {
	cfg  Config
	deps Deps

	baseCtx context.Context
	cancel  context.CancelFunc

	http *http.Server

	mu       sync.Mutex
	listener net.Listener
	inflight int
	started  bool
	stopping bool
}

// New 构造 Server 并校验配置与依赖，不进入监听。
//
// 配置非法（地址不可解析、时长/上限非正、非回环监听缺少凭证）或依赖缺失时返回错误，
// 调用方必须放弃启动（fail-closed）。
func New(cfg Config, deps Deps) (*Server, error) {
	normalized := cfg.withDefaults()
	if err := normalized.Validate(); err != nil {
		return nil, err
	}
	if deps.Sessions == nil {
		return nil, errors.New("httpapi: Deps.Sessions 不能为 nil")
	}
	if deps.Runner == nil {
		return nil, errors.New("httpapi: Deps.Runner 不能为 nil")
	}

	baseCtx, cancel := context.WithCancel(context.Background())
	s := &Server{
		cfg:     normalized,
		deps:    deps,
		baseCtx: baseCtx,
		cancel:  cancel,
	}
	s.http = &http.Server{
		// 单入口分派：不使用会做路径清理与重定向的默认 ServeMux 语义。
		Handler: s,
		// 每个请求的 context 都派生自服务根 context，关闭宽限期满时可统一取消在途 Run。
		BaseContext: func(net.Listener) context.Context { return s.baseCtx },
	}
	return s, nil
}

// Serve 在配置地址上监听并提供服务，阻塞直到服务退出。
//
// 监听成功后 Addr 才返回实际地址；监听失败立即返回错误且不进入服务状态。
// Shutdown / Close 导致的正常退出返回 nil。
func (s *Server) Serve() error {
	s.mu.Lock()
	if s.started || s.stopping {
		s.mu.Unlock()
		return errors.New("httpapi: 服务只能启动一次")
	}
	listener, err := net.Listen("tcp", s.cfg.ListenAddr)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("httpapi: 监听 %q 失败: %w", s.cfg.ListenAddr, err)
	}
	s.started = true
	s.listener = listener
	s.mu.Unlock()

	err = s.http.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("httpapi: 服务异常退出: %w", err)
	}
	return nil
}

// Addr 返回实际监听地址；尚未监听时返回空字符串。
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// ServeHTTP 按「方法 + 精确路径」分派三个端点；所有响应（含 404/405）都是 JSON 封套。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	release := s.beginRequest()
	defer release()

	switch r.URL.Path {
	case "/v1/sessions":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		s.handleCreateSession(w, r)
	case "/v1/chat":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		s.handleChat(w, r)
	case "/healthz":
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		s.handleHealthz(w, r)
	default:
		writeError(w, CodeNotFound)
	}
}

// Shutdown 优雅关闭：停止接受新连接并在 ctx 内等待在途请求完成。
//
// ctx 到期（宽限期满）时取消服务根 context（在途 Run 收到 cancelled），再有界等待
// 请求处理协程退出并强制关闭连接，保证关闭返回后不遗留仍在执行的 Run。
// 重复调用幂等且不 panic。
func (s *Server) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		return nil
	}
	s.stopping = true
	s.mu.Unlock()

	err := s.http.Shutdown(ctx)
	if err == nil {
		// 全部在途请求已完成，取消根 context 释放资源不影响任何 Run。
		s.cancel()
		return nil
	}

	s.cancel()
	s.waitIdle(s.cfg.ShutdownGrace)
	if closeErr := s.http.Close(); closeErr != nil && !errors.Is(closeErr, http.ErrServerClosed) {
		return errors.Join(err, closeErr)
	}
	return err
}

// Close 立即关闭：取消根 context 并关闭全部连接，不等待在途请求。
//
// 与 Shutdown 可混合/重复调用，幂等且不 panic。
func (s *Server) Close() error {
	s.mu.Lock()
	alreadyStopping := s.stopping
	s.stopping = true
	s.mu.Unlock()

	s.cancel()
	if alreadyStopping {
		// 关闭流程已在进行：连接由 Shutdown/上一次 Close 负责，这里只保证根 context 已取消。
		return nil
	}
	err := s.http.Close()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// beginRequest 记录在途请求，返回的释放函数必须在请求结束时调用。
func (s *Server) beginRequest() func() {
	s.mu.Lock()
	s.inflight++
	s.mu.Unlock()

	return func() {
		s.mu.Lock()
		s.inflight--
		s.mu.Unlock()
	}
}

// waitIdle 以有界轮询等待在途请求退出；超时后返回，不无限阻塞关闭流程。
func (s *Server) waitIdle(limit time.Duration) {
	if limit <= 0 {
		return
	}
	deadline := time.Now().Add(limit)
	for {
		s.mu.Lock()
		inflight := s.inflight
		s.mu.Unlock()
		if inflight == 0 || !time.Now().Before(deadline) {
			return
		}
		time.Sleep(idlePollInterval)
	}
}

// methodNotAllowed 写出 405 并声明该路径允许的方法。
func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeError(w, CodeMethodNotAllowed)
}
