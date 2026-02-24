package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// isValidOutputFilename checks that a filename contains only safe characters.
// Allowed: alphanumeric, dash, underscore, dot. No path separators or encoded chars.
func isValidOutputFilename(name string) bool {
	if len(name) > 255 {
		return false
	}
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	// Must not start with dot (hidden files).
	return len(name) > 0 && name[0] != '.'
}

// authMiddleware checks Bearer token on API endpoints.
// Skips auth for /healthz, /dashboard, and static assets.
// If token is empty, auth is disabled (backward compatible).
func authMiddleware(cfg *Config, secMon *securityMonitor, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cfg.APIToken == "" {
			next.ServeHTTP(w, r)
			return
		}

		// Skip auth for health check, metrics, dashboard, Slack events, WhatsApp webhook, Discord interactions, LINE webhook, Teams webhook, Signal webhook, Google Chat webhook, and iMessage webhook.
		p := r.URL.Path
		if p == "/healthz" || p == "/metrics" || p == "/dashboard" || strings.HasPrefix(p, "/dashboard/") || p == "/slack/events" || p == "/api/whatsapp/webhook" || p == "/api/discord/interactions" || strings.HasPrefix(p, "/api/line/") || strings.HasPrefix(p, "/api/teams/") || strings.HasPrefix(p, "/api/signal/") || strings.HasPrefix(p, "/api/gchat/") || strings.HasPrefix(p, "/api/imessage/") || p == "/api/docs" || p == "/api/spec" || strings.HasPrefix(p, "/hooks/") || (strings.HasPrefix(p, "/api/oauth/") && strings.HasSuffix(p, "/callback")) {
			next.ServeHTTP(w, r)
			return
		}

		// Allow requests with valid dashboard session cookie (same-origin API calls from dashboard).
		if cookie, err := r.Cookie("tetora_session"); err == nil {
			secret := cfg.DashboardAuth.Password
			if secret == "" {
				secret = cfg.DashboardAuth.Token
			}
			if secret != "" && validateDashboardCookie(cookie.Value, secret) {
				next.ServeHTTP(w, r)
				return
			}
		}

		// Allow same-origin requests from dashboard (Referer-based).
		if ref := r.Header.Get("Referer"); ref != "" {
			if strings.Contains(ref, "/dashboard") {
				next.ServeHTTP(w, r)
				return
			}
		}

		auth := r.Header.Get("Authorization")
		if auth == "" || auth != "Bearer "+cfg.APIToken {
			ip := clientIP(r)
			auditLog(cfg.HistoryDB, "api.auth.fail", "http", p, ip)
			if secMon != nil {
				secMon.recordEvent(ip, "auth.fail")
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}

		next.ServeHTTP(w, r)
	})
}

// dashboardAuthCookie generates a signed cookie value for dashboard auth.
func dashboardAuthCookie(secret string) string {
	// Sign a timestamp-based token: timestamp:hmac(timestamp, secret)
	ts := fmt.Sprintf("%d", time.Now().Unix())
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	sig := hex.EncodeToString(mac.Sum(nil))
	return ts + ":" + sig
}

// validateDashboardCookie checks if a dashboard auth cookie is valid and not expired (24h).
func validateDashboardCookie(cookie, secret string) bool {
	parts := strings.SplitN(cookie, ":", 2)
	if len(parts) != 2 {
		return false
	}
	ts := parts[0]
	sig := parts[1]

	// Verify HMAC.
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return false
	}

	// Check expiry (24h).
	tsInt, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	return time.Since(time.Unix(tsInt, 0)) < 24*time.Hour
}

// dashboardAuthMiddleware protects /dashboard paths when dashboard auth is enabled.
func dashboardAuthMiddleware(cfg *Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !cfg.DashboardAuth.Enabled {
			next.ServeHTTP(w, r)
			return
		}

		p := r.URL.Path
		// Only protect dashboard paths.
		if p != "/dashboard" && !strings.HasPrefix(p, "/dashboard/") {
			next.ServeHTTP(w, r)
			return
		}

		// Allow login page through.
		if p == "/dashboard/login" {
			next.ServeHTTP(w, r)
			return
		}

		// Allow PWA assets through.
		if p == "/dashboard/manifest.json" || p == "/dashboard/sw.js" || p == "/dashboard/icon.svg" {
			next.ServeHTTP(w, r)
			return
		}

		// Check cookie.
		secret := cfg.DashboardAuth.Password
		if secret == "" {
			secret = cfg.DashboardAuth.Token
		}
		if cookie, err := r.Cookie("tetora_session"); err == nil {
			if validateDashboardCookie(cookie.Value, secret) {
				next.ServeHTTP(w, r)
				return
			}
		}

		// Not authenticated — redirect to login.
		http.Redirect(w, r, "/dashboard/login", http.StatusFound)
	})
}

// --- Login Rate Limiter ---

type loginAttempt struct {
	failures int
	lastFail time.Time
}

type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string]*loginAttempt
}

const (
	loginMaxFailures = 5
	loginLockoutDur  = 15 * time.Minute
)

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{attempts: make(map[string]*loginAttempt)}
}

// isLocked returns true if the IP is currently locked out.
func (ll *loginLimiter) isLocked(ip string) bool {
	ll.mu.Lock()
	defer ll.mu.Unlock()
	a, ok := ll.attempts[ip]
	if !ok {
		return false
	}
	if a.failures >= loginMaxFailures && time.Since(a.lastFail) < loginLockoutDur {
		return true
	}
	// Lockout expired — reset.
	if a.failures >= loginMaxFailures {
		delete(ll.attempts, ip)
	}
	return false
}

// recordFailure records a failed login attempt for the given IP.
func (ll *loginLimiter) recordFailure(ip string) {
	ll.mu.Lock()
	defer ll.mu.Unlock()
	a, ok := ll.attempts[ip]
	if !ok {
		a = &loginAttempt{}
		ll.attempts[ip] = a
	}
	// Reset if lockout has expired.
	if a.failures >= loginMaxFailures && time.Since(a.lastFail) >= loginLockoutDur {
		a.failures = 0
	}
	a.failures++
	a.lastFail = time.Now()
}

// recordSuccess clears the failure count for the given IP.
func (ll *loginLimiter) recordSuccess(ip string) {
	ll.mu.Lock()
	defer ll.mu.Unlock()
	delete(ll.attempts, ip)
}

// cleanup removes expired entries. Called periodically to prevent memory leak.
func (ll *loginLimiter) cleanup() {
	ll.mu.Lock()
	defer ll.mu.Unlock()
	for ip, a := range ll.attempts {
		if time.Since(a.lastFail) >= loginLockoutDur {
			delete(ll.attempts, ip)
		}
	}
}

func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		return strings.TrimSpace(strings.SplitN(fwd, ",", 2)[0])
	}
	// Strip port from RemoteAddr.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// --- IP Allowlist ---

type ipAllowlist struct {
	ips   []net.IP
	cidrs []*net.IPNet
}

func parseAllowlist(entries []string) *ipAllowlist {
	if len(entries) == 0 {
		return nil
	}
	al := &ipAllowlist{}
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if strings.Contains(entry, "/") {
			_, cidr, err := net.ParseCIDR(entry)
			if err == nil {
				al.cidrs = append(al.cidrs, cidr)
			}
		} else {
			if ip := net.ParseIP(entry); ip != nil {
				al.ips = append(al.ips, ip)
			}
		}
	}
	return al
}

func (al *ipAllowlist) contains(ipStr string) bool {
	if al == nil {
		return true // no allowlist = allow all
	}
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	for _, allowed := range al.ips {
		if allowed.Equal(ip) {
			return true
		}
	}
	for _, cidr := range al.cidrs {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// ipAllowlistMiddleware rejects requests from IPs not in the allowlist.
// If allowlist is empty, all IPs are allowed (backward compatible).
func ipAllowlistMiddleware(al *ipAllowlist, dbPath string, next http.Handler) http.Handler {
	if al == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always allow healthz and metrics for monitoring probes.
		if r.URL.Path == "/healthz" || r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}

		ip := clientIP(r)
		if !al.contains(ip) {
			auditLog(dbPath, "api.ip.blocked", "http", r.URL.Path, ip)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"error":"forbidden"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- API Rate Limiter ---

type apiRateLimiter struct {
	mu      sync.Mutex
	windows map[string]*ipWindow
	limit   int // max requests per minute
}

type ipWindow struct {
	timestamps []time.Time
}

func newAPIRateLimiter(maxPerMin int) *apiRateLimiter {
	if maxPerMin <= 0 {
		maxPerMin = 60
	}
	return &apiRateLimiter{
		windows: make(map[string]*ipWindow),
		limit:   maxPerMin,
	}
}

// allow checks if the IP is under the rate limit.
func (rl *apiRateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-time.Minute)

	w, ok := rl.windows[ip]
	if !ok {
		w = &ipWindow{}
		rl.windows[ip] = w
	}

	// Trim old timestamps.
	start := 0
	for start < len(w.timestamps) && w.timestamps[start].Before(cutoff) {
		start++
	}
	w.timestamps = w.timestamps[start:]

	if len(w.timestamps) >= rl.limit {
		return false
	}

	w.timestamps = append(w.timestamps, now)
	return true
}

// cleanup removes expired entries to prevent memory leak.
func (rl *apiRateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	cutoff := time.Now().Add(-time.Minute)
	for ip, w := range rl.windows {
		// Remove IPs with no recent activity.
		if len(w.timestamps) == 0 || w.timestamps[len(w.timestamps)-1].Before(cutoff) {
			delete(rl.windows, ip)
		}
	}
}

// rateLimitMiddleware applies per-IP rate limiting to all API endpoints.
func rateLimitMiddleware(cfg *Config, rl *apiRateLimiter, next http.Handler) http.Handler {
	if !cfg.RateLimit.Enabled || rl == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip rate limiting for healthz, metrics, and static dashboard assets.
		p := r.URL.Path
		if p == "/healthz" || p == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}

		ip := clientIP(r)
		if !rl.allow(ip) {
			auditLog(cfg.HistoryDB, "api.ratelimit", "http", p, ip)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"rate limit exceeded"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- Async Route Results ---

type routeResultEntry struct {
	Result    *SmartDispatchResult `json:"result,omitempty"`
	Status    string               `json:"status"` // "running", "done", "error"
	Error     string               `json:"error,omitempty"`
	CreatedAt time.Time            `json:"createdAt"`
}

var (
	routeResultsMu sync.Mutex
	routeResults   = make(map[string]*routeResultEntry)
)

const routeResultTTL = 30 * time.Minute

func cleanupRouteResults() {
	routeResultsMu.Lock()
	defer routeResultsMu.Unlock()
	now := time.Now()
	for id, entry := range routeResults {
		if now.Sub(entry.CreatedAt) > routeResultTTL {
			delete(routeResults, id)
		}
	}
}

// recoveryMiddleware catches panics in HTTP handlers, logs the stack trace, and returns 500.
func recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rv := recover(); rv != nil {
				buf := make([]byte, 4096)
				n := runtime.Stack(buf, false)
				logError("http handler panic", "panic", fmt.Sprintf("%v", rv), "path", r.URL.Path, "stack", string(buf[:n]))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"error":"internal server error"}`))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// bodySizeMiddleware limits request body size to prevent resource exhaustion (10 MB).
func bodySizeMiddleware(next http.Handler) http.Handler {
	const maxBodySize = 10 << 20 // 10 MB
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBodySize)
		}
		next.ServeHTTP(w, r)
	})
}

func startHTTPServer(s *Server) *http.Server {
	s.startTime = time.Now()
	cfg := s.cfg
	mux := http.NewServeMux()
	s.limiter = newLoginLimiter()
	s.apiLimiter = newAPIRateLimiter(cfg.RateLimit.MaxPerMin)
	allowlist := parseAllowlist(cfg.AllowedIPs)

	// Initialize Canvas Engine.
	s.canvasEngine = newCanvasEngine(cfg, s.mcpHost)

	// Register canvas tools.
	if cfg.toolRegistry != nil {
		registerCanvasTools(cfg.toolRegistry, s.canvasEngine, cfg)
	}

	// Initialize Voice Realtime Engine (P16.2).
	s.voiceRealtimeEngine = newVoiceRealtimeEngine(cfg, s.voiceEngine)

	// Register all route groups.
	s.registerWebhookRoutes(mux)
	s.registerHealthRoutes(mux)
	s.registerDispatchRoutes(mux)
	s.registerCronRoutes(mux)
	s.registerHistoryRoutes(mux)
	s.registerStatsRoutes(mux)
	s.registerRoleRoutes(mux)
	s.registerMemoryRoutes(mux)
	s.registerSessionRoutes(mux)
	s.registerToolRoutes(mux)
	s.registerVoiceRoutes(mux)
	s.registerCanvasRoutes(mux)
	s.registerWorkflowRoutes(mux)
	s.registerAgentRoutes(mux)
	s.registerKnowledgeRoutes(mux)
	s.registerPushRoutes(mux)
	s.registerReminderRoutes(mux)
	s.registerAdminRoutes(mux)
	s.registerFamilyRoutes(mux)
	s.registerContactsRoutes(mux)
	s.registerHabitsRoutes(mux)

	// PWA assets.
	mux.HandleFunc("/dashboard/manifest.json", handlePWAManifest)
	mux.HandleFunc("/dashboard/sw.js", handlePWAServiceWorker)
	mux.HandleFunc("/dashboard/icon.svg", handlePWAIcon)

	// Dashboard.
	mux.HandleFunc("/dashboard", handleDashboard)

	// Middleware chain: recovery → trace → body size → rate limit → dashboard auth → IP allowlist → API auth → mux
	handler := recoveryMiddleware(traceMiddleware(bodySizeMiddleware(rateLimitMiddleware(cfg, s.apiLimiter,
		dashboardAuthMiddleware(cfg,
			ipAllowlistMiddleware(allowlist, cfg.HistoryDB,
				authMiddleware(cfg, s.secMon, mux)))))))

	srv := &http.Server{Addr: cfg.ListenAddr, Handler: handler}

	// Periodic cleanup for rate limiters + security monitor + async route results + failed tasks.
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			s.limiter.cleanup()
			s.apiLimiter.cleanup()
			if s.secMon != nil {
				s.secMon.cleanup()
			}
			cleanupRouteResults()
			cleanupFailedTasks(s.state)
		}
	}()

	// Start with TLS if configured.
	if cfg.tlsEnabled {
		srv.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
		go func() {
			if err := srv.ListenAndServeTLS(cfg.TLS.CertFile, cfg.TLS.KeyFile); err != http.ErrServerClosed {
				logError("https server error", "error", err)
			}
		}()
		logInfo("https server listening", "addr", cfg.ListenAddr)
	} else {
		go func() {
			if err := srv.ListenAndServe(); err != http.ErrServerClosed {
				logError("http server error", "error", err)
			}
		}()
		logInfo("http server listening", "addr", cfg.ListenAddr)
	}
	return srv
}

const dashboardLoginHTML = `<!DOCTYPE html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Tetora - Login</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:system-ui,sans-serif;background:#0a0a0f;color:#e0e0e0;display:flex;align-items:center;justify-content:center;min-height:100vh}
.card{background:#14141e;border:1px solid #2a2a3a;border-radius:12px;padding:2rem;width:320px}
h1{font-size:1.2rem;margin-bottom:1.5rem;text-align:center;color:#a78bfa}
input[type=password]{width:100%;padding:.6rem .8rem;background:#1a1a2e;border:1px solid #333;border-radius:6px;color:#e0e0e0;font-size:.9rem;margin-bottom:1rem}
input:focus{outline:none;border-color:#a78bfa}
button{width:100%;padding:.6rem;background:#a78bfa;color:#0a0a0f;border:none;border-radius:6px;font-size:.9rem;font-weight:600;cursor:pointer}
button:hover{background:#8b5cf6}
</style></head><body>
<div class="card"><h1>Tetora Dashboard</h1>
<form method="POST"><input type="password" name="password" placeholder="Password" autofocus required>
<button type="submit">Login</button></form></div></body></html>`

const dashboardLoginFailHTML = `<!DOCTYPE html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Tetora - Login</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:system-ui,sans-serif;background:#0a0a0f;color:#e0e0e0;display:flex;align-items:center;justify-content:center;min-height:100vh}
.card{background:#14141e;border:1px solid #2a2a3a;border-radius:12px;padding:2rem;width:320px}
h1{font-size:1.2rem;margin-bottom:1rem;text-align:center;color:#a78bfa}
.err{color:#f87171;font-size:.85rem;margin-bottom:1rem;text-align:center}
input[type=password]{width:100%;padding:.6rem .8rem;background:#1a1a2e;border:1px solid #333;border-radius:6px;color:#e0e0e0;font-size:.9rem;margin-bottom:1rem}
input:focus{outline:none;border-color:#a78bfa}
button{width:100%;padding:.6rem;background:#a78bfa;color:#0a0a0f;border:none;border-radius:6px;font-size:.9rem;font-weight:600;cursor:pointer}
button:hover{background:#8b5cf6}
</style></head><body>
<div class="card"><h1>Tetora Dashboard</h1>
<div class="err">Invalid password</div>
<form method="POST"><input type="password" name="password" placeholder="Password" autofocus required>
<button type="submit">Login</button></form></div></body></html>`

const dashboardLoginLockedHTML = `<!DOCTYPE html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Tetora - Login</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:system-ui,sans-serif;background:#0a0a0f;color:#e0e0e0;display:flex;align-items:center;justify-content:center;min-height:100vh}
.card{background:#14141e;border:1px solid #2a2a3a;border-radius:12px;padding:2rem;width:320px}
h1{font-size:1.2rem;margin-bottom:1rem;text-align:center;color:#a78bfa}
.err{color:#f87171;font-size:.85rem;margin-bottom:1rem;text-align:center}
</style></head><body>
<div class="card"><h1>Tetora Dashboard</h1>
<div class="err">Too many attempts, try again later</div></div></body></html>`
