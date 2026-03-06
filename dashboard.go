package main

import (
	"embed"
	"net/http"
	"strings"
)

//go:embed dashboard.html
var dashboardHTML []byte

//go:embed office_bg.webp
var officeBgWebp []byte

//go:embed sprite_ruri.png sprite_hisui.png sprite_kokuyou.png sprite_kohaku.png sprite_default.png
var spriteFS embed.FS

func handleDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(dashboardHTML)
}

func handleOfficeBg(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/webp")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write(officeBgWebp)
}

func handleSprite(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/dashboard/sprites/")
	if name == "" || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}
	data, err := spriteFS.ReadFile("sprite_" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// Detect content type from extension
	ct := "image/png"
	if strings.HasSuffix(name, ".webp") {
		ct = "image/webp"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write(data)
}

