package web

import (
	"embed"
	"net/http"
)

// 分离 HTML/CSS/JS 资源，避免单文件过大，也方便后续按功能维护管理页。
//
//go:embed index.html base.css console.css app.js
var assets embed.FS

func HandleIndex(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/":
		serveAsset(w, "index.html", "text/html; charset=utf-8")
	case "/assets/base.css":
		serveAsset(w, "base.css", "text/css; charset=utf-8")
	case "/assets/console.css":
		serveAsset(w, "console.css", "text/css; charset=utf-8")
	case "/assets/app.js":
		serveAsset(w, "app.js", "application/javascript; charset=utf-8")
	default:
		http.NotFound(w, r)
	}
}

func serveAsset(w http.ResponseWriter, name string, contentType string) {
	body, err := assets.ReadFile(name)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType)
	_, _ = w.Write(body)
}
