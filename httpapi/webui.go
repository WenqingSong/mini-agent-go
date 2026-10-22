package httpapi

import (
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// 前端静态产物的固定布局：Vite 生成的入口与内容哈希资源目录。
const (
	// webIndexFile 是 SPA 入口文件名。
	webIndexFile = "index.html"
	// webAssetDir 是 Vite 产物中的内容哈希资源目录（前缀匹配时需要斜杠）。
	webAssetDir = "assets/"
)

// webContentSecurityPolicy 与页面实际需要的资源类型一致：
// 脚本、样式、图片、字体与同源 fetch 之外一律禁止，且不允许被嵌入框架。
const webContentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; font-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"

// webContentTypes 覆盖构建产物中可能出现的扩展名。
//
// 容器运行阶段是 alpine，不一定装有 /etc/mime.types，因此不依赖系统的 MIME 数据库。
var webContentTypes = map[string]string{
	".css":         "text/css; charset=utf-8",
	".gif":         "image/gif",
	".html":        "text/html; charset=utf-8",
	".ico":         "image/x-icon",
	".jpeg":        "image/jpeg",
	".jpg":         "image/jpeg",
	".js":          "text/javascript; charset=utf-8",
	".json":        "application/json",
	".map":         "application/json",
	".mjs":         "text/javascript; charset=utf-8",
	".otf":         "font/otf",
	".png":         "image/png",
	".svg":         "image/svg+xml",
	".ttf":         "font/ttf",
	".txt":         "text/plain; charset=utf-8",
	".wasm":        "application/wasm",
	".webmanifest": "application/manifest+json",
	".webp":        "image/webp",
	".woff":        "font/woff",
	".woff2":       "font/woff2",
	".xml":         "text/xml; charset=utf-8",
}

// apiPathPrefixes 是必须按 API 语义处理的前缀：未命中的路径返回 JSON 404，
// 不会被 SPA fallback 变成 index.html。
var apiPathPrefixes = []string{"/v1", "/api"}

// browserRoute 是同源代理入口到真实 API 端点的固定映射。
type browserRoute struct {
	// target 是改写后的 API 路径。
	target string
	// method 是该端点唯一允许的方法。
	method string
}

// browserRoutes 是浏览器可访问的全部同源入口。
//
// 前端只访问 /api/*，认证凭证由本进程在服务端注入，因此既不需要浏览器持有凭证，
// 也不需要为浏览器放开 /v1 的认证要求。新增对外端点必须同时在此登记，
// 否则浏览器侧一律 404（默认拒绝）。
var browserRoutes = map[string]browserRoute{
	"/api/healthz":     {target: "/healthz", method: http.MethodGet},
	"/api/v1/sessions": {target: "/v1/sessions", method: http.MethodPost},
	"/api/v1/chat":     {target: "/v1/chat", method: http.MethodPost},
}

// browserRouteTarget 返回同源代理入口对应的 API 路径；非入口返回空字符串。
func browserRouteTarget(urlPath string) string {
	return browserRoutes[urlPath].target
}

// isAPIPath 判断路径是否属于 API 命名空间（含只存在于 API 命名空间内的未注册路径）。
func isAPIPath(urlPath string) bool {
	for _, prefix := range apiPathPrefixes {
		if urlPath == prefix || strings.HasPrefix(urlPath, prefix+"/") {
			return true
		}
	}
	return false
}

// serveBrowserAPI 处理前端同源 /api/* 请求。
//
// 处理顺序：入口登记 → 方法 → 来源校验 → 服务端注入凭证 → 复用 API 处理器。
// 复用同一套处理器意味着错误语义、请求体上限、超时与 Run 生命周期与 /v1 完全一致。
// 浏览器自带的 Authorization 会被覆盖：前端产物里没有凭证，也无法借此提升权限。
func (s *Server) serveBrowserAPI(w http.ResponseWriter, r *http.Request) {
	route, ok := browserRoutes[r.URL.Path]
	if !ok || s.web == nil {
		writeError(w, CodeNotFound)
		return
	}
	if r.Method != route.method {
		methodNotAllowed(w, route.method)
		return
	}
	// 写入型入口要求同源证据：跨站表单/脚本的 POST 在到达 Runner 之前就被拒绝。
	if route.method == http.MethodPost && !sameOriginRequest(r) {
		writeError(w, CodeForbiddenOrigin)
		return
	}

	r.URL.Path = route.target
	r.URL.RawPath = ""
	if s.cfg.AuthToken == "" {
		r.Header.Del("Authorization")
	} else {
		r.Header.Set("Authorization", bearerPrefix+s.cfg.AuthToken)
	}
	s.serveAPI(w, r)
}

// sameOriginRequest 判断写入请求是否来自同源页面。
//
// 优先使用 Fetch Metadata（浏览器保证该头不可被页面脚本伪造）：cross-site 一律拒绝；
// 旧浏览器没有该头时回退到 Origin / Host 比较；两者都没有时按同源放行（命令行调用）。
func sameOriginRequest(r *http.Request) bool {
	fetchSite := r.Header.Get("Sec-Fetch-Site")
	if fetchSite == "cross-site" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if fetchSite == "same-origin" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host)
}

// webUI 是前端静态产物目录的只读视图。
//
// 所有路径都先按 fs.ValidPath 归一化再解析，越出根目录的请求返回 404，
// 不存在任意文件读取的可能。
type webUI struct {
	// fs 是根目录的文件系统视图，路径只能相对该根解析。
	fs fs.FS
}

// newWebUI 校验静态目录；dir 为空表示不提供页面，返回 (nil, nil)。
// 目录不存在、不是目录或缺少 index.html 时返回错误：镜像装配不完整必须启动失败。
func newWebUI(dir string) (*webUI, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, nil
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("httpapi: 前端静态目录 %q 不可解析: %w", dir, err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("httpapi: 前端静态目录 %q 不可用: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("httpapi: 前端静态目录 %q 不是目录", dir)
	}
	ui := &webUI{fs: os.DirFS(root)}
	if _, err := fs.Stat(ui.fs, webIndexFile); err != nil {
		return nil, fmt.Errorf("httpapi: 前端静态目录 %q 缺少 %s（前端产物未构建或未拷贝）", dir, webIndexFile)
	}
	return ui, nil
}

// serveHTTP 提供静态资源与 SPA fallback。
//
// 命中真实文件时按其内容类型返回；未命中且路径没有扩展名时返回 index.html，
// 使用户刷新或直接访问页面路由不会 404；未命中但带扩展名时保持 404，
// 避免把缺失的 JS/CSS 请求变成带 HTML 内容的 200。
func (u *webUI) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	rel, ok := staticPath(r.URL.Path)
	if !ok {
		writeError(w, CodeNotFound)
		return
	}
	if rel != "" {
		if file, info, found := u.open(rel); found {
			defer file.Close()
			u.writeFile(w, r, rel, file, info)
			return
		}
		if path.Ext(rel) != "" {
			writeError(w, CodeNotFound)
			return
		}
	}
	if !u.writeIndex(w, r) {
		writeError(w, CodeNotFound)
	}
}

// staticPath 把 URL 路径归一化为静态根内的相对路径。
//
// 返回空字符串表示根（index.html）。含 ".." 段或归一化后仍非法的路径被拒绝，
// 因此 %2e%2e%2f 之类的编码穿越同样只会得到 404。
func staticPath(urlPath string) (string, bool) {
	if urlPath == "" || urlPath == "/" {
		return "", true
	}
	for _, segment := range strings.Split(urlPath, "/") {
		if segment == ".." {
			return "", false
		}
	}
	rel := strings.TrimPrefix(path.Clean("/"+urlPath), "/")
	if rel == "" {
		return "", true
	}
	if !fs.ValidPath(rel) {
		return "", false
	}
	return rel, true
}

// open 打开根目录内的普通文件；目录、缺失文件与非常规文件都视为未命中。
func (u *webUI) open(rel string) (fs.File, fs.FileInfo, bool) {
	file, err := u.fs.Open(rel)
	if err != nil {
		return nil, nil, false
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, nil, false
	}
	return file, info, true
}

// writeIndex 写出 SPA 入口；入口不可读时返回 false，由调用方映射为 404。
func (u *webUI) writeIndex(w http.ResponseWriter, r *http.Request) bool {
	file, info, ok := u.open(webIndexFile)
	if !ok {
		return false
	}
	defer file.Close()
	u.writeFile(w, r, webIndexFile, file, info)
	return true
}

// writeFile 写出单个静态文件：显式 MIME、缓存策略与安全响应头。
func (u *webUI) writeFile(w http.ResponseWriter, r *http.Request, rel string, file fs.File, info fs.FileInfo) {
	seeker, ok := file.(io.ReadSeeker)
	if !ok {
		writeError(w, CodeNotFound)
		return
	}
	header := w.Header()
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("Content-Security-Policy", webContentSecurityPolicy)
	if strings.HasPrefix(rel, webAssetDir) {
		// Vite 产物名含内容哈希：可长期缓存，更新镜像后新页面只会引用新文件名。
		header.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		// index.html 等非哈希文件每次都重新校验，避免新旧版本资源混用。
		header.Set("Cache-Control", "no-cache")
	}
	header.Set("Content-Type", contentTypeOf(rel))
	// ServeContent 负责 HEAD、Range 与条件请求（304），这里只提供已声明的类型与缓存头。
	http.ServeContent(w, r, path.Base(rel), info.ModTime(), seeker)
}

// contentTypeOf 返回静态文件的响应类型；未知扩展名退化为二进制流。
func contentTypeOf(rel string) string {
	extension := strings.ToLower(path.Ext(rel))
	if value, ok := webContentTypes[extension]; ok {
		return value
	}
	if value := mime.TypeByExtension(extension); value != "" {
		return value
	}
	return "application/octet-stream"
}
