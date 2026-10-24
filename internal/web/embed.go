// Package web 托管前端产物，并提供 SPA 回退。
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// 必须用 all: 前缀：构建产物里存在以 _ 和 . 开头的条目（如 Vite 的
// assets/ 下文件、.vite/manifest.json），而 go:embed 默认排除它们——
// 少了 all: 会在运行时才发现某些资源 404。
//
//go:embed all:dist
var embedded embed.FS

// HasBuild 判断是否内嵌了真实的前端产物。
func HasBuild() bool {
	b, err := embedded.ReadFile("dist/index.html")
	return err == nil && len(b) > 0
}

// FS 返回内嵌产物的文件系统。
func FS() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		return embedded
	}
	return sub
}

// Handler 提供 SPA 托管：已存在的静态文件直接服务，其余回退 index.html。
// 未构建前端时返回一个提示页，而不是 500——本地 go run 调试后端时很常见。
func Handler() http.Handler {
	if !HasBuild() {
		return http.HandlerFunc(devPlaceholder)
	}
	fileServer := http.FileServer(http.FS(FS()))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 静态资源（带哈希文件名）可长期缓存；HTML 必须每次校验。
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		// Safari 只认 application/manifest+json，而 Go 的
		// mime.TypeByExtension(".webmanifest") 返回空串。
		if strings.HasSuffix(r.URL.Path, ".webmanifest") {
			w.Header().Set("Content-Type", "application/manifest+json")
		}
		if f, err := FS().Open(strings.TrimPrefix(r.URL.Path, "/")); err == nil {
			_ = f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}

func devPlaceholder(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeErr(w, http.StatusNotFound, "接口不存在")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8">
<title>游履</title><style>body{font-family:-apple-system,"PingFang SC",sans-serif;
display:flex;align-items:center;justify-content:center;height:100vh;margin:0;background:#14161a;color:#e6e3dd}
div{max-width:32rem;padding:2rem;line-height:1.7}h1{font-size:1.25rem;margin:0 0 .75rem}
code{background:#23262c;padding:.15rem .4rem;border-radius:4px}p{color:#9aa0a6;font-size:.875rem}</style>
</head><body><div><h1>前端尚未构建</h1>
<p>当前二进制里没有内嵌前端产物。执行 <code>./scripts/build.sh</code> 重新构建，
或用 <code>cd web &amp;&amp; pnpm run dev</code> 起开发服务器。</p></div></body></html>`))
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(`{"error":"` + msg + `"}`))
}
