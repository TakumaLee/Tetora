package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"tetora/internal/audit"
	"tetora/internal/backup"
	tetoraConfig "tetora/internal/config"
	"tetora/internal/classify"
	"tetora/internal/cli"
	"tetora/internal/cost"
	"tetora/internal/db"
	"tetora/internal/discord"
	"tetora/internal/history"
	"tetora/internal/httpapi"
	"tetora/internal/httputil"
	"tetora/internal/knowledge"
	"tetora/internal/log"
	"tetora/internal/messaging/webhook"
	"tetora/internal/pairing"
	"tetora/internal/provider"
	"tetora/internal/pwa"
	"tetora/internal/quickaction"
	"tetora/internal/sla"
	"tetora/internal/sprite"
	"tetora/internal/store"
	"tetora/internal/team"
	"tetora/internal/trace"
	"tetora/internal/upload"
	"tetora/internal/version"
	"tetora/internal/voice"
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

// jsonError writes a JSON-encoded error response. Using json.Marshal to encode
// the message ensures the output is valid JSON even when the message contains
// quotes, backslashes, or other characters that would break a raw fmt.Sprintf.
func jsonError(w http.ResponseWriter, msg string, code int) {
	b, err := json.Marshal(struct {
		Error string `json:"error"`
	}{Error: msg})
	if err != nil {
		// Fallback: plain text — should never happen for a string value.
		http.Error(w, `{"error":"internal error"}`, code)
		return
	}
	http.Error(w, string(b), code)
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
		if p == "/" || p == "/healthz" || p == "/metrics" || p == "/dashboard" || strings.HasPrefix(p, "/dashboard/") || p == "/slack/events" || p == "/api/whatsapp/webhook" || p == "/api/discord/interactions" || strings.HasPrefix(p, "/api/line/") || strings.HasPrefix(p, "/api/teams/") || strings.HasPrefix(p, "/api/signal/") || strings.HasPrefix(p, "/api/gchat/") || strings.HasPrefix(p, "/api/imessage/") || p == "/api/docs" || p == "/api/spec" || strings.HasPrefix(p, "/hooks/") || isHooksPath(p) || (strings.HasPrefix(p, "/api/oauth/") && strings.HasSuffix(p, "/callback")) || strings.HasPrefix(p, "/api/callbacks/") {
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
			audit.Log(cfg.HistoryDB, "api.auth.fail", "http", p, ip)
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

// --- Multi-tenant client identification ---

// contextKey is used for context value keys to avoid collisions.
type contextKey string

const clientIDKey contextKey = "clientID"

// clientFromRequest extracts the client ID from the request.
// Falls back to the default client ID from config if not provided.
func clientFromRequest(r *http.Request, defaultID string) string {
	if id := r.Header.Get("X-Client-ID"); id != "" {
		return id
	}
	return defaultID
}

// isValidClientID validates that a client ID matches the expected format: cli_ prefix
// followed by 1-28 lowercase alphanumeric or hyphen characters.
func isValidClientID(id string) bool {
	if len(id) < 5 || len(id) > 32 { // "cli_" + 1..28
		return false
	}
	if id[:4] != "cli_" {
		return false
	}
	for _, c := range id[4:] {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			return false
		}
	}
	return true
}

// clientMiddleware extracts X-Client-ID from the request header, validates it,
// and stores it in the request context. If absent, uses the config default.
func clientMiddleware(defaultClientID string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientID := clientFromRequest(r, defaultClientID)
		if !isValidClientID(clientID) {
			http.Error(w, `{"error":"invalid client id"}`, http.StatusBadRequest)
			return
		}
		ctx := context.WithValue(r.Context(), clientIDKey, clientID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// getClientID extracts the client ID from request context (set by clientMiddleware).
func getClientID(r *http.Request) string {
	if id, ok := r.Context().Value(clientIDKey).(string); ok {
		return id
	}
	return "cli_default"
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
		if p == "/dashboard/manifest.json" || p == "/dashboard/sw.js" || p == "/dashboard/icon.svg" || p == "/dashboard/office-bg.webp" || strings.HasPrefix(p, "/dashboard/sprites/") {
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

func clientIP(r *http.Request) string { return httputil.ClientIP(r) }

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
			audit.Log(dbPath, "api.ip.blocked", "http", r.URL.Path, ip)
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
			audit.Log(cfg.HistoryDB, "api.ratelimit", "http", p, ip)
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
				log.Error("http handler panic", "panic", fmt.Sprintf("%v", rv), "path", r.URL.Path, "stack", string(buf[:n]))
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
	if cfg.Runtime.ToolRegistry != nil {
		registerCanvasTools(cfg.Runtime.ToolRegistry.(*ToolRegistry), s.canvasEngine, cfg)
	}

	// Initialize Voice Realtime Engine (P16.2).
	s.voiceRealtimeEngine = newVoiceRealtimeEngine(cfg, s.voiceEngine)

	// Register all route groups.
	{
		var handlers []httpapi.WebhookHandler
		if s.slackBot != nil {
			handlers = append(handlers, httpapi.WebhookHandler{Path: "/slack/events", Handler: s.slackBot.EventHandler})
		}
		if s.whatsappBot != nil {
			handlers = append(handlers, httpapi.WebhookHandler{Path: "/api/whatsapp/webhook", Handler: s.whatsappBot.WebhookHandler})
		}
		if s.state.discordBot != nil && cfg.Discord.PublicKey != "" {
			discordBot := s.state.discordBot
			handlers = append(handlers, httpapi.WebhookHandler{Path: "/api/discord/interactions", Handler: func(w http.ResponseWriter, r *http.Request) {
				handleDiscordInteraction(discordBot, w, r)
			}})
		}
		if s.lineBot != nil {
			handlers = append(handlers, httpapi.WebhookHandler{Path: cfg.LINE.WebhookPathOrDefault(), Handler: s.lineBot.HandleWebhook})
		}
		if s.teamsBot != nil {
			handlers = append(handlers, httpapi.WebhookHandler{Path: "/api/teams/webhook", Handler: s.teamsBot.HandleWebhook})
		}
		if s.signalBot != nil {
			handlers = append(handlers, httpapi.WebhookHandler{Path: cfg.Signal.WebhookPathOrDefault(), Handler: s.signalBot.HandleWebhook})
		}
		if s.gchatBot != nil {
			handlers = append(handlers, httpapi.WebhookHandler{Path: cfg.GoogleChat.WebhookPathOrDefault(), Handler: s.gchatBot.HandleWebhook})
		}
		if s.imessageBot != nil {
			handlers = append(handlers, httpapi.WebhookHandler{Path: cfg.IMessage.WebhookPathOrDefault(), Handler: s.imessageBot.WebhookHandler})
		}
		httpapi.RegisterWebhookRoutes(mux, httpapi.WebhookDeps{Handlers: handlers})
	}
	httpapi.RegisterHealthRoutes(mux, httpapi.HealthDeps{
		StartTime: s.startTime,
		HistoryDB: cfg.HistoryDB,
		DefaultProvider: func() string {
			provider := cfg.DefaultProvider
			if provider == "" && len(cfg.Agents) > 0 {
				for _, a := range cfg.Agents {
					if a.Model != "" {
						provider = a.Model
						break
					}
				}
			}
			return provider
		},
		GetRunningAgents: func() ([]map[string]any, bool) {
			s.state.mu.Lock()
			defer s.state.mu.Unlock()
			draining := s.state.draining
			agents := make([]map[string]any, 0, len(s.state.running))
			for _, ts := range s.state.running {
				info := map[string]any{
					"id":      ts.task.ID,
					"name":    ts.task.Name,
					"agent":   ts.task.Agent,
					"source":  ts.task.Source,
					"elapsed": time.Since(ts.startAt).Round(time.Second).String(),
					"stalled": ts.stalled,
				}
				if !ts.lastActivity.IsZero() {
					info["silent"] = time.Since(ts.lastActivity).Round(time.Second).String()
				}
				agents = append(agents, info)
			}
			return agents, draining
		},
		SSEClientCount: func() int {
			if s.state != nil && s.state.broker != nil {
				return s.state.broker.ClientCount()
			}
			return -1
		},
		LastCronRun: func() time.Time {
			if s.cron != nil {
				return s.cron.LastRunTime()
			}
			return time.Time{}
		},
		DeepCheck: func() map[string]any {
			checks := deepHealthCheck(cfg, s.state, s.cron, s.startTime)
			if len(s.DegradedServices) > 0 {
				checks["degradedServices"] = s.DegradedServices
				if st, ok := checks["status"].(string); ok {
					checks["status"] = degradeStatus(st, "degraded")
				}
			}
			if s.heartbeatMonitor != nil {
				stats := s.heartbeatMonitor.Stats()
				hbInfo := map[string]any{
					"enabled":         true,
					"checkCount":      stats.CheckCount,
					"stallsDetected":  stats.StallsDetected,
					"stallsRecovered": stats.StallsRecovered,
					"autoCancelled":   stats.AutoCancelled,
					"timeoutWarnings": stats.TimeoutWarnings,
				}
				if !stats.LastCheck.IsZero() {
					hbInfo["lastCheck"] = stats.LastCheck.Format(time.RFC3339)
				}
				stalledCount := 0
				s.state.mu.Lock()
				for _, ts := range s.state.running {
					if ts.stalled {
						stalledCount++
					}
				}
				s.state.mu.Unlock()
				hbInfo["stalledNow"] = stalledCount
				if stalledCount > 0 {
					if st, ok := checks["status"].(string); ok {
						checks["status"] = degradeStatus(st, "degraded")
					}
				}
				checks["heartbeat"] = hbInfo
			} else {
				checks["heartbeat"] = map[string]any{"enabled": false}
			}
			return checks
		},
		WriteMetrics: func(w http.ResponseWriter) bool {
			if metricsGlobal == nil {
				return false
			}
			metricsGlobal.WriteMetrics(w)
			return true
		},
		CircuitRegistry: cfg.Runtime.CircuitRegistry,
	})
	s.registerDispatchRoutes(mux)
	httpapi.RegisterCronRoutes(mux, httpapi.CronDeps{
		Available:    s.cron != nil,
		ListJobs:     func() any { return s.cron.ListJobs() },
		AddJob: func(raw json.RawMessage) error {
			var jc CronJobConfig
			if err := json.Unmarshal(raw, &jc); err != nil {
				return err
			}
			if jc.ID == "" || jc.Schedule == "" {
				return fmt.Errorf("id and schedule are required")
			}
			return s.cron.AddJob(jc)
		},
		GetJobConfig: func(id string) any { return s.cron.GetJobConfig(id) },
		UpdateJob: func(id string, raw json.RawMessage) error {
			var jc CronJobConfig
			if err := json.Unmarshal(raw, &jc); err != nil {
				return err
			}
			if jc.Schedule == "" {
				return fmt.Errorf("schedule is required")
			}
			return s.cron.UpdateJob(id, jc)
		},
		RemoveJob:  func(id string) error { return s.cron.RemoveJob(id) },
		ToggleJob:  func(id string, enabled bool) error { return s.cron.ToggleJob(id, enabled) },
		ApproveJob: func(id string) error { return s.cron.ApproveJob(id) },
		RejectJob:  func(id string) error { return s.cron.RejectJob(id) },
		RunJob:     func(ctx context.Context, id string) error { return s.cron.RunJobByID(ctx, id) },
		HistoryDB:  func() string { return s.Cfg().HistoryDB },
	})
	httpapi.RegisterHistoryRoutes(mux, func(r *http.Request) string {
		cfg := s.Cfg()
		return s.resolveHistoryDB(cfg, getClientID(r))
	})
	httpapi.RegisterStatsRoutes(mux, httpapi.StatsDeps{
		HistoryDB: cfg.HistoryDB,
		QueryCostStats: func(dbPath string) (today, week, month float64, err error) {
			stats, e := history.QueryCostStats(dbPath)
			return stats.Today, stats.Week, stats.Month, e
		},
		QueryDailyStats: func(dbPath string, days int) (any, error) {
			return history.QueryDailyStats(dbPath, days)
		},
		QueryMetricsSummary: func(dbPath string, days int) (any, error) {
			return history.QueryMetrics(dbPath, days)
		},
		QueryDailyMetrics: func(dbPath string, days int) (any, error) {
			return history.QueryDailyMetrics(dbPath, days)
		},
		QueryProviderMetrics: func(dbPath string, days int) (any, error) {
			return history.QueryProviderMetrics(dbPath, days)
		},
		CostAlertDailyLimit:  func() float64 { return s.Cfg().CostAlert.DailyLimit },
		CostAlertWeeklyLimit: func() float64 { return s.Cfg().CostAlert.WeeklyLimit },
		CostAlertAction:      func() string { return s.Cfg().CostAlert.Action },
		Budgets:              func() cost.BudgetConfig { return s.Cfg().Budgets },
		SetBudgetPaused:      setBudgetPaused,
		ConfigPath:           func() string { return s.Cfg().BaseDir },
		SLAConfig:            func() sla.SLAConfig { return s.Cfg().SLA },
		AgentNames: func() []string {
			c := s.Cfg()
			names := make([]string, 0, len(c.Agents))
			for name := range c.Agents {
				names = append(names, name)
			}
			return names
		},
		QueryUsageSummary: func(dbPath, period string) (*httpapi.UsageSummary, error) {
			s, err := queryUsageSummary(dbPath, period)
			if err != nil {
				return nil, err
			}
			return &httpapi.UsageSummary{
				Period:     s.Period,
				TotalCost:  s.TotalCost,
				TotalTasks: s.TotalTasks,
				TokensIn:   s.TokensIn,
				TokensOut:  s.TokensOut,
			}, nil
		},
		QueryUsageByModel: func(dbPath string, days int) (any, error) {
			return queryUsageByModel(dbPath, days)
		},
		QueryUsageByAgent: func(dbPath string, days int) (any, error) {
			return queryUsageByAgent(dbPath, days)
		},
		QueryExpensiveSessions: func(dbPath string, limit, days int) (any, error) {
			return queryExpensiveSessions(dbPath, limit, days)
		},
		QueryCostTrend: func(dbPath string, days int) (any, error) {
			return queryCostTrend(dbPath, days)
		},
	})
	// --- Agent Routes ---
	// TaskBoard init: must happen here before RegisterAgentRoutes.
	var taskBoardEngine *TaskBoardEngine
	if cfg.TaskBoard.Enabled {
		taskBoardEngine = newTaskBoardEngine(cfg.HistoryDB, cfg.TaskBoard, cfg.Webhooks)
		if err := taskBoardEngine.InitSchema(); err != nil {
			log.Error("init task board schema failed", "error", err)
		}
		if cfg.TaskBoard.AutoDispatch.Enabled {
			disp := newTaskBoardDispatcher(taskBoardEngine, cfg, s.sem, s.childSem, s.state)
			disp.Start()
			s.taskBoardDispatcher = disp
		}
	}
	// QuickAction engine init.
	quickActionEngine := quickaction.NewEngine(cfg.QuickActions, cfg.SmartDispatch.DefaultAgent)

	httpapi.RegisterAgentRoutes(mux, httpapi.AgentDeps{
		HistoryDB: cfg.HistoryDB,
		QueryAgentMessages: func(workflowRun, role string, limit int) (any, error) {
			msgs, err := queryAgentMessages(cfg.HistoryDB, workflowRun, role, limit)
			if msgs == nil {
				msgs = []AgentMessage{}
			}
			return msgs, err
		},
		SendAgentMessage: func(body json.RawMessage) (string, error) {
			var msg AgentMessage
			if err := json.Unmarshal(body, &msg); err != nil {
				return "", err
			}
			if msg.Type == "" {
				msg.Type = "note"
			}
			if err := sendAgentMessage(cfg.HistoryDB, msg); err != nil {
				return "", err
			}
			return msg.ID, nil
		},
		QueryHandoffs: func(workflowRun string) (any, error) {
			handoffs, err := queryHandoffs(cfg.HistoryDB, workflowRun)
			if handoffs == nil {
				handoffs = []Handoff{}
			}
			return handoffs, err
		},
		TaskBoardEnabled: cfg.TaskBoard.Enabled,
		ListTasksPaginated: func(status, assignee, project string, page, limit int) (any, error) {
			if taskBoardEngine == nil {
				return nil, fmt.Errorf("task board not enabled")
			}
			result, err := taskBoardEngine.ListTasksPaginated(status, assignee, project, page, limit)
			if err != nil {
				return nil, err
			}
			if result.Tasks == nil {
				result.Tasks = []TaskBoard{}
			}
			return result, nil
		},
		CreateTask: func(body json.RawMessage) (any, error) {
			if taskBoardEngine == nil {
				return nil, fmt.Errorf("task board not enabled")
			}
			var task TaskBoard
			if err := json.Unmarshal(body, &task); err != nil {
				return nil, err
			}
			return taskBoardEngine.CreateTask(task)
		},
		GetTask: func(id string) (any, error) {
			if taskBoardEngine == nil {
				return nil, fmt.Errorf("task board not enabled")
			}
			return taskBoardEngine.GetTask(id)
		},
		UpdateTask: func(id string, body json.RawMessage) (any, error) {
			if taskBoardEngine == nil {
				return nil, fmt.Errorf("task board not enabled")
			}
			var updates map[string]any
			if err := json.Unmarshal(body, &updates); err != nil {
				return nil, err
			}
			return taskBoardEngine.UpdateTask(id, updates)
		},
		DeleteTask: func(id string) error {
			if taskBoardEngine == nil {
				return fmt.Errorf("task board not enabled")
			}
			return taskBoardEngine.DeleteTask(id)
		},
		MoveTask: func(id, status string) (any, error) {
			if taskBoardEngine == nil {
				return nil, fmt.Errorf("task board not enabled")
			}
			return taskBoardEngine.MoveTask(id, status)
		},
		AssignTask: func(id, assignee string) (any, error) {
			if taskBoardEngine == nil {
				return nil, fmt.Errorf("task board not enabled")
			}
			return taskBoardEngine.AssignTask(id, assignee)
		},
		GetBoardView: func(params map[string]string) (any, error) {
			if taskBoardEngine == nil {
				return nil, fmt.Errorf("task board not enabled")
			}
			includeDone := params["includeDone"] == "true"
			return taskBoardEngine.GetBoardView(BoardFilter{
				Project:     params["project"],
				Assignee:    params["assignee"],
				Priority:    params["priority"],
				Workflow:    params["workflow"],
				IncludeDone: includeDone,
			})
		},
		ListChildren: func(parentID string) (any, error) {
			if taskBoardEngine == nil {
				return nil, fmt.Errorf("task board not enabled")
			}
			return taskBoardEngine.ListChildren(parentID)
		},
		AddComment: func(taskID, author, content, ctype string) (any, error) {
			if taskBoardEngine == nil {
				return nil, fmt.Errorf("task board not enabled")
			}
			return taskBoardEngine.AddComment(taskID, author, content, ctype)
		},
		GetThread: func(taskID string) (any, error) {
			if taskBoardEngine == nil {
				return nil, fmt.Errorf("task board not enabled")
			}
			comments, err := taskBoardEngine.GetThread(taskID)
			if comments == nil {
				comments = []TaskComment{}
			}
			return comments, err
		},
		PublishBoardUpdate: func(data map[string]any) {
			if s.state != nil && s.state.broker != nil {
				s.state.broker.Publish(SSEDashboardKey, SSEEvent{Type: "board_updated", Data: data})
			}
		},
		ListQuickActions: func() any {
			return quickActionEngine.List()
		},
		RunQuickAction: func(ctx context.Context, name string, params map[string]any) (any, error) {
			prompt, role, err := quickActionEngine.BuildPrompt(name, params)
			if err != nil {
				return nil, err
			}
			task := Task{
				Name:   "quick:" + name,
				Prompt: prompt,
				Agent:  role,
				Source: "quick:" + name,
			}
			fillDefaults(cfg, &task)
			tasks := []Task{task}
			result := dispatch(ctx, cfg, tasks, s.state, s.sem, s.childSem)
			if len(result.Tasks) == 0 {
				return nil, fmt.Errorf("no result")
			}
			return result.Tasks[0], nil
		},
		SearchQuickActions: func(query string) any {
			return quickActionEngine.Search(query)
		},
		ListAgents: func(ctx context.Context) (string, error) {
			return toolAgentList(ctx, cfg, json.RawMessage(`{}`))
		},
		GetAgentMessages: func(role string, markAsRead bool) (any, error) {
			return getAgentMessages(cfg.HistoryDB, role, markAsRead)
		},
		SendAgentMsg: func(ctx context.Context, body json.RawMessage) (string, error) {
			return toolAgentMessage(ctx, cfg, body)
		},
		GetRunningAgents: func() any {
			type runningTask struct {
				ID       string `json:"id"`
				Name     string `json:"name"`
				Agent    string `json:"agent,omitempty"`
				Source   string `json:"source,omitempty"`
				Prompt   string `json:"prompt,omitempty"`
				Elapsed  string `json:"elapsed"`
				ParentID string `json:"parentId,omitempty"`
				Depth    int    `json:"depth,omitempty"`
			}
			var tasks []runningTask
			if s.state != nil {
				s.state.mu.Lock()
				for _, ts := range s.state.running {
					prompt := ts.task.Prompt
					if len(prompt) > 100 {
						prompt = prompt[:100] + "..."
					}
					tasks = append(tasks, runningTask{
						ID:       ts.task.ID,
						Name:     ts.task.Name,
						Agent:    ts.task.Agent,
						Source:   ts.task.Source,
						Prompt:   prompt,
						Elapsed:  time.Since(ts.startAt).Round(time.Second).String(),
						ParentID: ts.task.ParentID,
						Depth:    ts.task.Depth,
					})
				}
				s.state.mu.Unlock()
			}
			if tasks == nil {
				tasks = []runningTask{}
			}
			return map[string]any{"running": tasks, "count": len(tasks)}
		},
		GetAllTrustStatuses: func() any {
			return getAllTrustStatuses(cfg)
		},
		GetTrustStatus: func(agent string) any {
			return getTrustStatus(cfg, agent)
		},
		AgentExists: func(name string) bool {
			_, ok := cfg.Agents[name]
			return ok
		},
		SetTrustLevel: func(agent, level, ip string) (any, error) {
			oldLevel := resolveTrustLevel(cfg, agent)
			if err := updateAgentTrustLevel(cfg, agent, level); err != nil {
				return nil, err
			}
			configPath := filepath.Join(cfg.BaseDir, "config.json")
			if err := saveAgentTrustLevel(configPath, agent, level); err != nil {
				log.Warn("persist trust level failed", "agent", agent, "error", err)
			}
			recordTrustEvent(cfg.HistoryDB, agent, "set", oldLevel, level, 0, "set via API")
			audit.Log(cfg.HistoryDB, "trust.set", "http",
				fmt.Sprintf("agent=%s from=%s to=%s", agent, oldLevel, level), ip)
			return getTrustStatus(cfg, agent), nil
		},
		ValidTrustLevels: func() []string {
			return validTrustLevels
		},
		IsValidTrustLevel: func(level string) bool {
			return isValidTrustLevel(level)
		},
		QueryTrustEvents: func(role string, limit int) (any, error) {
			events, err := queryTrustEvents(cfg.HistoryDB, role, limit)
			if events == nil {
				events = []map[string]any{}
			}
			return events, err
		},
		AuditLog: func(action, source, detail, ip string) {
			audit.Log(cfg.HistoryDB, action, source, detail, ip)
		},
	})
	httpapi.RegisterMemoryRoutes(mux, httpapi.MemoryDeps{
		ListMCPConfigs:  func() any { return listMCPConfigs(cfg) },
		GetMCPConfig:    func(name string) (json.RawMessage, error) { return getMCPConfig(cfg, name) },
		SetMCPConfig:    func(configPath, name string, raw json.RawMessage) error { return setMCPConfig(cfg, configPath, name, raw) },
		DeleteMCPConfig: func(configPath, name string) error { return deleteMCPConfig(cfg, configPath, name) },
		TestMCPConfig:   testMCPConfig,
		ListMemory:      func(role string) (any, error) { return listMemory(cfg, role) },
		GetMemory:       func(role, key string) (string, error) { return getMemory(cfg, role, key) },
		SetMemory:       func(agent, key, value string) error { return setMemory(cfg, agent, key, value) },
		DeleteMemory:    func(role, key string) error { return deleteMemory(cfg, role, key) },
		FindConfigPath:  findConfigPath,
		HistoryDB:       func() string { return s.Cfg().HistoryDB },
	})
	httpapi.RegisterSessionRoutes(mux, httpapi.SessionDeps{
		HistoryDB: cfg.HistoryDB,
		QuerySessions: func(role, status, source string, limit, offset int) (any, int, error) {
			q := SessionQuery{Agent: role, Status: status, Source: source, Limit: limit, Offset: offset}
			sessions, total, err := querySessions(cfg.HistoryDB, q)
			return sessions, total, err
		},
		CreateSession: func(agent, title, ip string) (any, error) {
			now := time.Now().Format(time.RFC3339)
			sess := Session{
				ID:        newUUID(),
				Agent:     agent,
				Source:    "chat",
				Status:    "active",
				Title:     title,
				CreatedAt: now,
				UpdatedAt: now,
			}
			if sess.Title == "" {
				sess.Title = "New chat with " + agent
			}
			if err := createSession(cfg.HistoryDB, sess); err != nil {
				return nil, err
			}
			audit.Log(cfg.HistoryDB, "session.create", "http",
				fmt.Sprintf("session=%s role=%s", sess.ID, sess.Agent), ip)
			return sess, nil
		},
		GetSessionDetail: func(id string) (any, error) {
			detail, err := querySessionDetail(cfg.HistoryDB, id)
			if err != nil {
				return nil, err
			}
			if detail == nil {
				return nil, nil
			}
			return detail, nil
		},
		ArchiveSession: func(id string) error {
			return updateSessionStatus(cfg.HistoryDB, id, "archived")
		},
		SendMessage: func(r *http.Request, sessionID, prompt string, async bool) (any, int, error) {
			sess, err := querySessionByID(cfg.HistoryDB, sessionID)
			if err != nil || sess == nil {
				return nil, http.StatusNotFound, fmt.Errorf("session not found")
			}

			// Pre-record user message immediately.
			now := time.Now().Format(time.RFC3339)
			if err := addSessionMessage(cfg.HistoryDB, SessionMessage{
				SessionID: sessionID,
				Role:      "user",
				Content:   truncateStr(prompt, 5000),
				CreatedAt: now,
			}); err != nil {
				log.Warn("add user message failed", "session", sessionID, "error", err)
			}
			if err := updateSessionStats(cfg.HistoryDB, sessionID, 0, 0, 0, 1); err != nil {
				log.Warn("update session stats failed", "session", sessionID, "error", err)
			}
			// Detect user corrections and record as lessons.
			go detectAndRecordCorrection(cfg.HistoryDB, cfg.WorkspaceDir, sessionID, sess.Agent, prompt)

			// Update session title on first message.
			title := prompt
			if len(title) > 100 {
				title = title[:100]
			}
			if err := updateSessionTitle(cfg.HistoryDB, sessionID, title); err != nil {
				log.Warn("update session title failed", "session", sessionID, "error", err)
			}

			// Re-activate session if it was completed.
			if sess.Status == "completed" {
				if err := updateSessionStatus(cfg.HistoryDB, sessionID, "active"); err != nil {
					log.Warn("reactivate session failed", "session", sessionID, "error", err)
				}
			}

			task := Task{
				Prompt:    prompt,
				Agent:     sess.Agent,
				SessionID: sessionID,
				Source:    "chat",
			}
			fillDefaults(cfg, &task)
			task.SessionID = sessionID // Override fillDefaults' new UUID.

			if async {
				taskID := task.ID
				traceID := trace.IDFromContext(r.Context())

				go func() {
					asyncCtx := trace.WithID(context.Background(), traceID)
					result := runTask(asyncCtx, cfg, task, s.state)

					nowDone := time.Now().Format(time.RFC3339)
					msgRole := "assistant"
					content := truncateStr(result.Output, 5000)
					if result.Status != "success" {
						msgRole = "system"
						errMsg := result.Error
						if errMsg == "" {
							errMsg = result.Status
						}
						content = fmt.Sprintf("[%s] %s", result.Status, truncateStr(errMsg, 2000))
					}
					addSessionMessage(cfg.HistoryDB, SessionMessage{
						SessionID: sessionID,
						Role:      msgRole,
						Content:   content,
						CostUSD:   result.CostUSD,
						TokensIn:  result.TokensIn,
						TokensOut: result.TokensOut,
						Model:     result.Model,
						TaskID:    task.ID,
						CreatedAt: nowDone,
					})
					updateSessionStats(cfg.HistoryDB, sessionID, result.CostUSD, result.TokensIn, result.TokensOut, 1)
				}()

				audit.Log(cfg.HistoryDB, "session.message.async", "http",
					fmt.Sprintf("session=%s role=%s task=%s", sessionID, sess.Agent, taskID), clientIP(r))
				return map[string]any{
					"taskId":    taskID,
					"sessionId": sessionID,
					"status":    "running",
				}, http.StatusAccepted, nil
			}

			// Sync mode — decouple from HTTP lifecycle.
			taskCtx := trace.WithID(context.Background(), trace.IDFromContext(r.Context()))
			result := runSingleTask(taskCtx, cfg, task, s.sem, s.childSem, sess.Agent)
			taskStart := time.Now().Add(-time.Duration(result.DurationMs) * time.Millisecond)
			recordHistory(cfg.HistoryDB, task.ID, task.Name, task.Source, sess.Agent, task, result,
				taskStart.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)

			nowDone := time.Now().Format(time.RFC3339)
			msgRole := "assistant"
			content := truncateStr(result.Output, 5000)
			if result.Status != "success" {
				msgRole = "system"
				errMsg := result.Error
				if errMsg == "" {
					errMsg = result.Status
				}
				content = fmt.Sprintf("[%s] %s", result.Status, truncateStr(errMsg, 2000))
			}
			addSessionMessage(cfg.HistoryDB, SessionMessage{
				SessionID: sessionID,
				Role:      msgRole,
				Content:   content,
				CostUSD:   result.CostUSD,
				TokensIn:  result.TokensIn,
				TokensOut: result.TokensOut,
				Model:     result.Model,
				TaskID:    task.ID,
				CreatedAt: nowDone,
			})
			updateSessionStats(cfg.HistoryDB, sessionID, result.CostUSD, result.TokensIn, result.TokensOut, 1)

			audit.Log(cfg.HistoryDB, "session.message", "http",
				fmt.Sprintf("session=%s role=%s", sessionID, sess.Agent), clientIP(r))
			return result, http.StatusOK, nil
		},
		MirrorMessage: func(r *http.Request, sessionID string, body json.RawMessage) (any, int, error) {
			var req struct {
				Role           string  `json:"role"`
				Content        string  `json:"content"`
				Model          string  `json:"model"`
				Cost           float64 `json:"cost"`
				TokensIn       int     `json:"tokensIn"`
				TokensOut      int     `json:"tokensOut"`
				DiscordChannel string  `json:"discordChannel"`
			}
			if err := json.Unmarshal(body, &req); err != nil {
				return nil, http.StatusBadRequest, fmt.Errorf("invalid JSON")
			}
			if req.Role == "" || req.Content == "" {
				return nil, http.StatusBadRequest, fmt.Errorf("role and content required")
			}
			if req.Role != "user" && req.Role != "assistant" && req.Role != "system" {
				return nil, http.StatusBadRequest, fmt.Errorf("role must be user, assistant, or system")
			}

			existingSess, err := querySessionByID(cfg.HistoryDB, sessionID)
			if err != nil {
				return nil, http.StatusInternalServerError, err
			}
			now := time.Now().Format(time.RFC3339)
			if existingSess == nil {
				newSess := Session{
					ID:        sessionID,
					Agent:     "mirror",
					Source:    "mirror",
					Status:    "active",
					Title:     "Mirror session",
					CreatedAt: now,
					UpdatedAt: now,
				}
				if err := createSession(cfg.HistoryDB, newSess); err != nil {
					return nil, http.StatusInternalServerError, err
				}
			}

			if err := addSessionMessage(cfg.HistoryDB, SessionMessage{
				SessionID: sessionID,
				Role:      req.Role,
				Content:   truncateStr(req.Content, 10000),
				CostUSD:   req.Cost,
				TokensIn:  req.TokensIn,
				TokensOut: req.TokensOut,
				Model:     req.Model,
				CreatedAt: now,
			}); err != nil {
				return nil, http.StatusInternalServerError, err
			}
			updateSessionStats(cfg.HistoryDB, sessionID, req.Cost, req.TokensIn, req.TokensOut, 1)

			if s.state.broker != nil {
				event := SSEEvent{
					Type:      SSEOutputChunk,
					SessionID: sessionID,
					Data: map[string]any{
						"role":    req.Role,
						"content": req.Content,
					},
					Timestamp: now,
				}
				publishToSSEBroker(s.state.broker, event)
			}

			if req.DiscordChannel != "" && cfg.Discord.Enabled && cfg.Discord.BotToken != "" {
				go func() {
					content := req.Content
					if len(content) > 1900 {
						content = content[:1900] + "\n..."
					}
					prefix := "💬"
					if req.Role == "assistant" {
						prefix = "🤖"
					}
					msg := fmt.Sprintf("%s **[mirror:%s]**\n%s", prefix, req.Role, content)
					if err := cronDiscordSendBotChannel(cfg.Discord.BotToken, req.DiscordChannel, msg); err != nil {
						log.Warn("mirror discord forward failed", "session", sessionID, "error", err)
					}
				}()
			}

			audit.Log(cfg.HistoryDB, "session.mirror", "http",
				fmt.Sprintf("session=%s role=%s len=%d", sessionID, req.Role, len(req.Content)), clientIP(r))
			return map[string]any{
				"status":    "ok",
				"sessionId": sessionID,
			}, http.StatusOK, nil
		},
		CompactSession: func(sessionID string) {
			go func() {
				compactCtx, compactCancel := context.WithTimeout(context.Background(), 2*time.Minute)
				defer compactCancel()
				if err := compactSession(compactCtx, cfg, cfg.HistoryDB, sessionID, false, s.sem, s.childSem); err != nil {
					log.Error("compact session error", "session", sessionID, "error", err)
				}
			}()
		},
		ServeSSE: func(w http.ResponseWriter, r *http.Request, sessionID string) {
			serveSSE(w, r, s.state.broker, sessionID)
		},
		ServeSSEPersistent: func(w http.ResponseWriter, r *http.Request, sessionID string) {
			serveSSEPersistent(w, r, s.state.broker, sessionID)
		},
		SSEAvailable: func() bool {
			return s.state.broker != nil
		},
		ListSkills: func() any {
			return listSkills(cfg)
		},
		RunSkill: func(r *http.Request, name string, vars map[string]string) (any, error) {
			skill := getSkill(cfg, name)
			if skill == nil {
				return nil, fmt.Errorf("skill %q not found", name)
			}
			return executeSkill(r.Context(), *skill, vars)
		},
		TestSkill: func(r *http.Request, name string) (any, error) {
			skill := getSkill(cfg, name)
			if skill == nil {
				return nil, fmt.Errorf("skill %q not found", name)
			}
			return testSkill(r.Context(), *skill)
		},
		SkillExists: func(name string) bool {
			return getSkill(cfg, name) != nil
		},
		AgentExists: func(name string) bool {
			_, ok := cfg.Agents[name]
			return ok
		},
	})
	httpapi.RegisterToolRoutes(mux, httpapi.ToolsDeps{
		ListTools: func() any {
			if cfg.Runtime.ToolRegistry == nil {
				return []any{}
			}
			tools := cfg.Runtime.ToolRegistry.(*ToolRegistry).List()
			result := make([]map[string]any, 0, len(tools))
			for _, t := range tools {
				var schema map[string]any
				if len(t.InputSchema) > 0 {
					json.Unmarshal(t.InputSchema, &schema)
				}
				result = append(result, map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"inputSchema": schema,
					"builtin":     t.Builtin,
					"requireAuth": t.RequireAuth,
				})
			}
			return result
		},
		MCPStatus: func() any {
			if s.mcpHost == nil {
				return []any{}
			}
			return s.mcpHost.ServerStatus()
		},
		MCPRestart: func(name string) error {
			if s.mcpHost == nil {
				return fmt.Errorf("MCP host not enabled")
			}
			return s.mcpHost.RestartServer(name)
		},
		HybridSearch:    func(ctx context.Context, query, source string, topK int) (any, error) { return hybridSearch(ctx, cfg, query, source, topK) },
		ReindexAll:      func(ctx context.Context) error { return reindexAll(ctx, cfg) },
		EmbeddingStatus: func() (any, error) { return embeddingStatus(cfg.HistoryDB) },
		ProactiveEnabled: s.proactiveEngine != nil,
		ListProactiveRules: func() any {
			if s.proactiveEngine == nil {
				return []any{}
			}
			return s.proactiveEngine.ListRules()
		},
		TriggerProactiveRule: func(name string) error {
			if s.proactiveEngine == nil {
				return fmt.Errorf("proactive engine not enabled")
			}
			return s.proactiveEngine.TriggerRule(name)
		},
		GroupChatEnabled: s.groupChatEngine != nil,
		GroupChatStatus: func() any {
			if s.groupChatEngine == nil {
				return nil
			}
			return s.groupChatEngine.Status()
		},
		HandleAPIDocs: handleAPIDocs,
		HandleAPISpec: handleAPISpec(cfg),
	})
	httpapi.RegisterVoiceRoutes(mux, httpapi.VoiceDeps{
		STTEnabled:       s.voiceEngine != nil && s.voiceEngine.STT != nil,
		TTSEnabled:       s.voiceEngine != nil && s.voiceEngine.TTS != nil,
		WakeEnabled:      cfg.Voice.Wake.Enabled,
		RealtimeEnabled:  cfg.Voice.Realtime.Enabled,
		DefaultTTSFormat: cfg.Voice.TTS.Format,
		Transcribe: func(ctx context.Context, audio io.Reader, opts httpapi.VoiceTranscribeOpts) (any, error) {
			return s.voiceEngine.Transcribe(ctx, audio, STTOptions{Language: opts.Language, Format: opts.Format})
		},
		Synthesize: func(ctx context.Context, text string, opts httpapi.VoiceSynthesizeOpts) (io.ReadCloser, error) {
			return s.voiceEngine.Synthesize(ctx, text, TTSOptions{Voice: opts.Voice, Speed: opts.Speed, Format: opts.Format})
		},
		HandleWakeWS:     func(w http.ResponseWriter, r *http.Request) { s.voiceRealtimeEngine.HandleWakeWebSocket(w, r) },
		HandleRealtimeWS: func(w http.ResponseWriter, r *http.Request) {
			var reg voice.ToolRegistryIface
			if cfg.Runtime.ToolRegistry != nil {
				reg = &toolRegistryAdapter{cfg: cfg, reg: cfg.Runtime.ToolRegistry.(*ToolRegistry)}
			}
			s.voiceRealtimeEngine.HandleRealtimeWebSocket(w, r, reg)
		},
	})
	httpapi.RegisterCanvasRoutes(mux, httpapi.CanvasDeps{
		ListSessions: func() (any, int) {
			sessions := s.canvasEngine.listCanvasSessions()
			return sessions, len(sessions)
		},
		GetSession:   func(id string) (any, error) { return s.canvasEngine.getCanvas(id) },
		SendMessage:  s.canvasEngine.handleCanvasMessage,
		CloseSession: s.canvasEngine.closeCanvas,
	})
	// --- Workflow Routes ---
	mutateTriggerConfig := func(mutate func(raw map[string]any)) error {
		configPath := findConfigPath()
		if configPath == "" {
			return fmt.Errorf("config path not found")
		}
		if err := updateConfigField(configPath, mutate); err != nil {
			return err
		}
		signalSelfReload()
		return nil
	}
	httpapi.RegisterWorkflowRoutes(mux, httpapi.WorkflowDeps{
		HistoryDB: func() string { return cfg.HistoryDB },
		APIToken:  func() string { return cfg.APIToken },
		ListWorkflows: func() (any, error) {
			wfs, err := listWorkflows(cfg)
			if wfs == nil {
				wfs = []*Workflow{}
			}
			return wfs, err
		},
		SaveWorkflow: func(body json.RawMessage) (string, int, []string, error) {
			var wf Workflow
			if err := json.Unmarshal(body, &wf); err != nil {
				return "", 0, nil, err
			}
			errs := validateWorkflow(&wf)
			if len(errs) > 0 {
				return "", 0, errs, nil
			}
			if err := saveWorkflow(cfg, &wf); err != nil {
				return "", 0, nil, err
			}
			return wf.Name, len(wf.Steps), nil, nil
		},
		LoadWorkflow: func(name string) (any, error) {
			return loadWorkflowByName(cfg, name)
		},
		DeleteWorkflow: func(name string) error {
			return deleteWorkflow(cfg, name)
		},
		ExportWorkflow: func(name string) (any, error) {
			wf, err := loadWorkflowByName(cfg, name)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"tetoraExport": "workflow/v1",
				"exportedAt":   time.Now().UTC().Format(time.RFC3339),
				"workflow":     wf,
			}, nil
		},
		ValidateWorkflow: func(name string) (string, any, []string, error) {
			wf, err := loadWorkflowByName(cfg, name)
			if err != nil {
				return "", nil, nil, err
			}
			errs := validateWorkflow(wf)
			if len(errs) > 0 {
				return wf.Name, nil, errs, nil
			}
			return wf.Name, topologicalSort(wf.Steps), nil, nil
		},
		RunWorkflow: func(ctx context.Context, name string, vars map[string]string) {
			wf, err := loadWorkflowByName(cfg, name)
			if err != nil {
				log.Warn("RunWorkflow: load failed", "name", name, "error", err)
				return
			}
			wfTraceID := trace.IDFromContext(ctx)
			go executeWorkflow(trace.WithID(context.Background(), wfTraceID), cfg, wf, vars, s.state, s.sem, s.childSem)
		},
		DryRunWorkflow: func(ctx context.Context, name string, vars map[string]string) (any, error) {
			wf, err := loadWorkflowByName(cfg, name)
			if err != nil {
				return nil, err
			}
			return executeWorkflow(ctx, cfg, wf, vars, s.state, s.sem, s.childSem, WorkflowModeDryRun), nil
		},
		ResumeWorkflow: func(ctx context.Context, runID string) {
			wfTraceID := trace.IDFromContext(ctx)
			go func() {
				run, err := resumeWorkflow(trace.WithID(context.Background(), wfTraceID), cfg, runID, s.state, s.sem, s.childSem, nil)
				if err != nil {
					log.Warn("workflow resume failed", "originalRunID", runID, "error", err)
				} else {
					log.Info("workflow resume dispatched", "originalRunID", runID, "newRunID", run.ID[:8])
				}
			}()
		},
		CancelRun: func(runID string) {
			if cancel, ok := runCancellers.Load(runID); ok {
				cancel.(context.CancelFunc)()
				runCancellers.Delete(runID)
			}
		},
		RestoreWorkflowVersion: func(historyDB, versionID string) error {
			return restoreWorkflowVersion(historyDB, cfg, versionID)
		},
		QueryWorkflowRuns: func(historyDB string, limit int, name string) (any, error) {
			runs, err := queryWorkflowRuns(historyDB, limit, name)
			if runs == nil {
				runs = []WorkflowRun{}
			}
			return runs, err
		},
		QueryWorkflowRunByID: func(historyDB, runID string) (any, error) {
			return queryWorkflowRunByID(historyDB, runID)
		},
		IsResumableStatus: func(status string) bool {
			return isResumableStatus(status)
		},
		QueryHandoffs: func(historyDB, runID string) (any, error) {
			handoffs, err := queryHandoffs(historyDB, runID)
			if handoffs == nil {
				handoffs = []Handoff{}
			}
			return handoffs, err
		},
		QueryAgentMessages: func(historyDB, runID string, limit int) (any, error) {
			msgs, err := queryAgentMessages(historyDB, runID, "", limit)
			if msgs == nil {
				msgs = []AgentMessage{}
			}
			return msgs, err
		},
		ImportWorkflow: func(body json.RawMessage) (string, int, []string, error) {
			var pkg struct {
				TetoraExport string          `json:"tetoraExport"`
				Workflow     Workflow        `json:"workflow"`
				Skill        *SkillMetadata  `json:"skill"`
				SkillDoc     string          `json:"skillDoc"`
			}
			if err := json.Unmarshal(body, &pkg); err != nil {
				return "", 0, nil, err
			}
			if pkg.TetoraExport == "" {
				return "", 0, []string{"not a valid Tetora export package (missing tetoraExport field)"}, nil
			}

			// Handle skill-workflow bundles.
			if pkg.TetoraExport == "skill-workflow/v1" {
				if pkg.Skill == nil {
					return "", 0, []string{"skill-workflow/v1 package missing skill field"}, nil
				}
				meta := *pkg.Skill
				if meta.Name == "" {
					return "", 0, []string{"skill name is required"}, nil
				}
				// Save the bundled workflow first.
				wf := &pkg.Workflow
				if wf.Name != "" {
					errs := validateWorkflow(wf)
					if len(errs) > 0 {
						return "", 0, errs, nil
					}
					if err := saveWorkflow(cfg, wf); err != nil {
						return "", 0, nil, fmt.Errorf("save workflow failed: %w", err)
					}
				}
				// Create the skill (empty script — workflow type).
				if err := createSkill(cfg, meta, ""); err != nil {
					return "", 0, nil, fmt.Errorf("create skill failed: %w", err)
				}
				// Write SKILL.md if provided.
				if pkg.SkillDoc != "" {
					skillMDPath := filepath.Join(skillsDir(cfg), meta.Name, "SKILL.md")
					if err := os.WriteFile(skillMDPath, []byte(pkg.SkillDoc), 0o644); err != nil {
						log.Warn("import: failed to write SKILL.md", "skill", meta.Name, "error", err)
					}
				}
				stepCount := 0
				if wf := &pkg.Workflow; wf.Name != "" {
					stepCount = len(wf.Steps)
				}
				return meta.Name, stepCount, nil, nil
			}

			wf := &pkg.Workflow
			if wf.Name == "" {
				return "", 0, []string{"workflow name is required"}, nil
			}
			errs := validateWorkflow(wf)
			if len(errs) > 0 {
				return "", 0, errs, nil
			}
			if err := saveWorkflow(cfg, wf); err != nil {
				return "", 0, nil, fmt.Errorf("save failed: %w", err)
			}
			return wf.Name, len(wf.Steps), nil, nil
		},
		StoreBrowse: func() ([]byte, error) {
			deps := store.Deps{
				ListWorkflows: func() ([]store.WorkflowInfo, error) {
					wfs, err := listWorkflows(cfg)
					if err != nil || wfs == nil {
						return nil, err
					}
					infos := make([]store.WorkflowInfo, len(wfs))
					for i, wf := range wfs {
						infos[i] = store.WorkflowInfo{
							Name:        wf.Name,
							Description: wf.Description,
							StepCount:   len(wf.Steps),
						}
					}
					return infos, nil
				},
				ListTemplates: func() []store.TemplateInfo {
					ts := listTemplates()
					infos := make([]store.TemplateInfo, len(ts))
					for i, t := range ts {
						infos[i] = store.TemplateInfo{
							Name:        t.Name,
							Description: t.Description,
							Category:    t.Category,
							StepCount:   t.StepCount,
							Variables:   t.Variables,
						}
					}
					return infos
				},
			}
			items, cats := store.Browse(deps)
			return store.ItemsToJSON(items, cats)
		},
		ListTemplates: func() (any, int) {
			ts := listTemplates()
			if ts == nil {
				ts = []TemplateSummary{}
			}
			return ts, len(ts)
		},
		LoadTemplate: func(name string) (any, error) {
			return loadTemplate(name)
		},
		InstallTemplate: func(name, newName string) error {
			return installTemplate(cfg, name, newName)
		},
		ListSkillInfos: func() []httpapi.SkillInfo {
			dir := skillsDir(cfg)
			entries, err := os.ReadDir(dir)
			if err != nil {
				return nil
			}
			var skills []httpapi.SkillInfo
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				name := e.Name()
				desc := ""
				metaPath := filepath.Join(dir, name, "metadata.json")
				if data, rerr := os.ReadFile(metaPath); rerr == nil {
					var meta struct {
						Description string `json:"description"`
					}
					if json.Unmarshal(data, &meta) == nil {
						desc = meta.Description
					}
				}
				if desc == "" {
					skillPath := filepath.Join(dir, name, "SKILL.md")
					if data, rerr := os.ReadFile(skillPath); rerr == nil {
						content := string(data)
						if strings.HasPrefix(content, "---\n") {
							if end := strings.Index(content[4:], "\n---"); end >= 0 {
								fm := content[4 : 4+end]
								for _, line := range strings.Split(fm, "\n") {
									line = strings.TrimSpace(line)
									if strings.HasPrefix(line, "description:") {
										desc = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
										desc = strings.Trim(desc, "\"'")
										break
									}
								}
							}
						}
					}
				}
				skills = append(skills, httpapi.SkillInfo{Name: name, Description: desc})
			}
			return skills
		},
		TriggerEngineAvailable: s.triggerEngine != nil,
		ListTriggers: func() (any, int) {
			if s.triggerEngine == nil {
				return []TriggerInfo{}, 0
			}
			infos := s.triggerEngine.ListTriggers()
			if infos == nil {
				infos = []TriggerInfo{}
			}
			return infos, len(infos)
		},
		HandleWebhookTrigger: func(webhookID string, payload map[string]string) error {
			if s.triggerEngine == nil {
				return fmt.Errorf("no triggers configured")
			}
			return s.triggerEngine.HandleWebhookTrigger(webhookID, payload)
		},
		FireTrigger: func(name string) error {
			if s.triggerEngine == nil {
				return fmt.Errorf("no triggers configured")
			}
			return s.triggerEngine.FireTrigger(name)
		},
		GetCurrentTriggerNames: func() []string {
			currentCfg := s.Cfg()
			var names []string
			for _, t := range currentCfg.WorkflowTriggers {
				names = append(names, t.Name)
			}
			return names
		},
		ValidateTriggerConfig: func(body json.RawMessage, existingNames map[string]bool) []string {
			var t WorkflowTriggerConfig
			if err := json.Unmarshal(body, &t); err != nil {
				return []string{fmt.Sprintf("invalid JSON: %v", err)}
			}
			return validateTriggerConfig(t, existingNames)
		},
		MutateTriggerConfig: mutateTriggerConfig,
		DecodeTriggerConfig: func(body json.RawMessage) (string, string, string, error) {
			var t WorkflowTriggerConfig
			if err := json.Unmarshal(body, &t); err != nil {
				return "", "", "", err
			}
			return t.Name, t.Trigger.Type, t.WorkflowName, nil
		},
		QueryTriggerRuns: func(historyDB, name string, limit int) ([]map[string]any, error) {
			return queryTriggerRuns(historyDB, name, limit)
		},
		IsValidCallbackKey: isValidCallbackKey,
		QueryPendingCallback: func(historyDB, key string) *httpapi.PendingCallbackRecord {
			rec := queryPendingCallback(historyDB, key)
			if rec == nil {
				return nil
			}
			return &httpapi.PendingCallbackRecord{Status: rec.Status, AuthMode: rec.AuthMode}
		},
		QueryPendingCallbackByKey: func(historyDB, key string) *httpapi.PendingCallbackRecord {
			rec := queryPendingCallbackByKey(historyDB, key)
			if rec == nil {
				return nil
			}
			return &httpapi.PendingCallbackRecord{Status: rec.Status, AuthMode: rec.AuthMode}
		},
		CallbackSignatureSecret: callbackSignatureSecret,
		VerifyCallbackSignature: verifyCallbackSignature,
		DeliverCallback: func(key string, payload httpapi.CallbackPayload) httpapi.DeliverCallbackResult {
			if callbackMgr == nil {
				return httpapi.DeliverCallbackResult{Outcome: "no_entry"}
			}
			result := CallbackResult{
				Status:      payload.Status,
				Body:        payload.Body,
				ContentType: payload.ContentType,
				RecvAt:      payload.RecvAt,
			}
			out := callbackMgr.DeliverAndSeq(key, result)
			var outcome string
			switch out.Result {
			case DeliverOK:
				outcome = "ok"
			case DeliverDup:
				outcome = "dup"
			case DeliverFull:
				outcome = "full"
			default:
				outcome = "no_entry"
			}
			return httpapi.DeliverCallbackResult{Outcome: outcome, Mode: out.Mode, Seq: out.Seq}
		},
		AppendStreamingCallback: func(historyDB, key string, seq int, payload httpapi.CallbackPayload) {
			appendStreamingCallback(historyDB, key, seq, CallbackResult{
				Status:      payload.Status,
				Body:        payload.Body,
				ContentType: payload.ContentType,
				RecvAt:      payload.RecvAt,
			})
		},
		MarkCallbackDelivered: func(historyDB, key string, seq int, payload httpapi.CallbackPayload) {
			markCallbackDelivered(historyDB, key, seq, CallbackResult{
				Status:      payload.Status,
				Body:        payload.Body,
				ContentType: payload.ContentType,
				RecvAt:      payload.RecvAt,
			})
		},
	})
	httpapi.RegisterHumanGateRoutes(mux, httpapi.HumanGateDeps{
		HistoryDB: func() string { return cfg.HistoryDB },
		QueryHumanGates: func(status string) []map[string]any {
			where := ""
			if status != "" {
				where = fmt.Sprintf(" WHERE status='%s'", db.Escape(status))
			}
			rows, err := db.Query(cfg.HistoryDB, fmt.Sprintf(
				`SELECT key, run_id as runId, step_id as stepId, workflow_name as workflowName,
				        subtype, COALESCE(prompt,'') as prompt, COALESCE(assignee,'') as assignee, status,
				        COALESCE(decision,'') as action, COALESCE(response,'') as response,
				        COALESCE(responded_by,'') as respondedBy, COALESCE(timeout_at,'') as timeoutAt,
				        COALESCE(created_at,'') as createdAt, COALESCE(completed_at,'') as completedAt
				 FROM workflow_human_gates%s ORDER BY created_at DESC`, where))
			if err != nil || rows == nil {
				return []map[string]any{}
			}
			return rows
		},
		CountHumanGates: func() int {
			return countPendingHumanGates(cfg.HistoryDB)
		},
		QueryHumanGateByKey: func(key string) map[string]any {
			rows, err := db.Query(cfg.HistoryDB, fmt.Sprintf(
				`SELECT key, run_id as runId, step_id as stepId, workflow_name as workflowName,
				        subtype, COALESCE(prompt,'') as prompt, COALESCE(assignee,'') as assignee, status,
				        COALESCE(decision,'') as action, COALESCE(response,'') as response,
				        COALESCE(responded_by,'') as respondedBy, COALESCE(timeout_at,'') as timeoutAt,
				        COALESCE(created_at,'') as createdAt, COALESCE(completed_at,'') as completedAt
				 FROM workflow_human_gates WHERE key='%s' LIMIT 1`, db.Escape(key)))
			if err != nil || len(rows) == 0 {
				return nil
			}
			return rows[0]
		},
		RespondHumanGate: func(key, action, response, respondedBy string) error {
			if callbackMgr == nil {
				// No live workflow manager — write directly to DB so the gate
				// is picked up when the workflow resumes.
				if action == "reject" || action == "rejected" {
					rejectHumanGate(cfg.HistoryDB, key, response, respondedBy)
				} else {
					completeHumanGate(cfg.HistoryDB, key, action, response, respondedBy)
				}
				return nil
			}
			// Build the callback body the workflow executor expects.
			bodyJSON, _ := json.Marshal(map[string]string{
				"action":      action,
				"response":    response,
				"respondedBy": respondedBy,
			})
			dr := callbackMgr.Deliver(key, CallbackResult{Body: string(bodyJSON)})
			if dr == DeliverNoEntry {
				// Workflow not currently waiting — write DB so resume picks it up.
				if action == "reject" || action == "rejected" {
					rejectHumanGate(cfg.HistoryDB, key, response, respondedBy)
				} else {
					completeHumanGate(cfg.HistoryDB, key, action, response, respondedBy)
				}
			}
			return nil
		},
		RetryHumanGate: func(key string, overrideVars map[string]string) (string, error) {
			return retryFromHumanGate(context.Background(), cfg, key, overrideVars, s.state, s.sem, s.childSem)
		},
		CancelHumanGate: func(key, reason, cancelledBy string) error {
			if callbackMgr == nil {
				cancelHumanGate(cfg.HistoryDB, key)
				return nil
			}
			bodyJSON, _ := json.Marshal(map[string]string{
				"action":      "cancelled",
				"response":    reason,
				"respondedBy": cancelledBy,
			})
			dr := callbackMgr.Deliver(key, CallbackResult{Body: string(bodyJSON)})
			if dr == DeliverNoEntry {
				// Workflow not currently waiting — write DB directly.
				cancelHumanGate(cfg.HistoryDB, key)
			}
			return nil
		},
	})
	httpapi.RegisterAgentRoleRoutes(mux, httpapi.AgentRoleDeps{
		ListArchetypes: func() []httpapi.ArchetypeInfo {
			out := make([]httpapi.ArchetypeInfo, len(builtinArchetypes))
			for i, a := range builtinArchetypes {
				out[i] = httpapi.ArchetypeInfo{
					Name:           a.Name,
					Description:    a.Description,
					Model:          a.Model,
					PermissionMode: a.PermissionMode,
					SoulTemplate:   a.SoulTemplate,
				}
			}
			return out
		},
		ListAgents: func() []httpapi.AgentInfo {
			cfg := s.cfg
			var roles []httpapi.AgentInfo
			for name, rc := range cfg.Agents {
				ri := httpapi.AgentInfo{
					Name:           name,
					Model:          rc.Model,
					Provider:       rc.Provider,
					PermissionMode: rc.PermissionMode,
					SoulFile:       rc.SoulFile,
					Description:    rc.Description,
					PortraitURL:    resolvePortraitURL(cfg.BaseDir, name),
				}
				if content, err := loadAgentPrompt(cfg, name); err == nil && content != "" {
					if len(content) > 500 {
						ri.SoulPreview = content[:500] + "..."
					} else {
						ri.SoulPreview = content
					}
				}
				roles = append(roles, ri)
			}
			return roles
		},
		AgentExists: func(name string) bool {
			_, ok := s.cfg.Agents[name]
			return ok
		},
		GetAgent: func(name string) (map[string]any, bool) {
			cfg := s.cfg
			rc, ok := cfg.Agents[name]
			if !ok {
				return nil, false
			}
			result := map[string]any{
				"name":           name,
				"model":          rc.Model,
				"provider":       rc.Provider,
				"permissionMode": rc.PermissionMode,
				"soulFile":       rc.SoulFile,
				"description":    rc.Description,
			}
			if content, err := loadAgentPrompt(cfg, name); err == nil {
				result["soulContent"] = content
			}
			return result, true
		},
		CreateAgent: func(name, model, permMode, desc, soulFile, soulContent string) error {
			cfg := s.cfg
			if soulContent != "" {
				if err := writeSoulFile(cfg, name, soulContent); err != nil {
					return fmt.Errorf("write soul file: %w", err)
				}
			}
			rc := AgentConfig{
				SoulFile:       soulFile,
				Model:          model,
				Description:    desc,
				PermissionMode: permMode,
			}
			configPath := findConfigPath()
			rcJSON, err := json.Marshal(&rc)
			if err != nil {
				return fmt.Errorf("marshal config: %w", err)
			}
			if err := cli.UpdateConfigAgents(configPath, name, rcJSON); err != nil {
				return fmt.Errorf("save config: %w", err)
			}
			if cfg.Agents == nil {
				cfg.Agents = make(map[string]AgentConfig)
			}
			cfg.Agents[name] = rc
			return nil
		},
		UpdateAgent: func(name, model, permMode, desc, soulFile, soulContent, providerName string) error {
			cfg := s.cfg
			rc := cfg.Agents[name]
			if model != "" {
				rc.Model = model
			}
			if permMode != "" {
				rc.PermissionMode = permMode
			}
			if desc != "" {
				rc.Description = desc
			}
			if soulFile != "" {
				rc.SoulFile = soulFile
			}
			if providerName != "" {
				rc.Provider = providerName
			}
			if soulContent != "" {
				if err := writeSoulFile(cfg, name, soulContent); err != nil {
					return fmt.Errorf("write soul: %w", err)
				}
			}
			configPath := findConfigPath()
			rcJSON, err := json.Marshal(&rc)
			if err != nil {
				return fmt.Errorf("marshal config: %w", err)
			}
			if err := cli.UpdateConfigAgents(configPath, name, rcJSON); err != nil {
				return fmt.Errorf("save: %w", err)
			}
			cfg.Agents[name] = rc
			return nil
		},
		DeleteAgent: func(name string) (error, bool) {
			cfg := s.cfg
			cron := s.cron
			if cron != nil {
				for _, j := range cron.ListJobs() {
					if j.Agent == name {
						return fmt.Errorf("agent in use by job %q", j.ID), true
					}
				}
			}
			configPath := findConfigPath()
			if err := cli.UpdateConfigAgents(configPath, name, nil); err != nil {
				return fmt.Errorf("save: %w", err), false
			}
			delete(cfg.Agents, name)
			return nil, false
		},
		HistoryDB: func() string { return s.cfg.HistoryDB },
	})

	// Team Builder routes.
	teamStore := team.NewStorage(cfg.BaseDir)
	httpapi.RegisterTeamRoutes(mux, httpapi.TeamDeps{
		Store:      teamStore,
		ConfigPath: filepath.Join(cfg.BaseDir, "config.json"),
		AgentsDir: func() string {
			if cfg.AgentsDir != "" {
				return cfg.AgentsDir
			}
			return filepath.Join(cfg.BaseDir, "agents")
		}(),
		SignalReload: signalSelfReload,
		GenerateTeam: func(ctx context.Context, req team.GenerateRequest) (*team.TeamDef, error) {
			reg, ok := s.Cfg().Runtime.ProviderRegistry.(*providerRegistry)
			if !ok || reg == nil {
				return nil, fmt.Errorf("no provider registry available")
			}
			// Use default provider or fall back to first available.
			providerName := s.Cfg().DefaultProvider
			if providerName == "" {
				providerName = "openai"
			}
			p, err := reg.Get(providerName)
			if err != nil {
				return nil, fmt.Errorf("provider %q: %w", providerName, err)
			}
			model := "opus"
			gen := team.NewGenerator(p, model)
			return gen.Generate(ctx, req)
		},
	})

	httpapi.RegisterKnowledgeRoutes(mux, httpapi.KnowledgeDeps{
		KnowledgeDir: func() string {
			cfg := s.Cfg()
			if cfg.KnowledgeDir != "" {
				return cfg.KnowledgeDir
			}
			return knowledge.InitDir(cfg.BaseDir)
		},
		HistoryDB: func() string { return s.Cfg().HistoryDB },
		SearchKnowledge: func(dir, query string, limit int) ([]httpapi.KnowledgeSearchResult, error) {
			idx, err := knowledge.BuildIndex(dir)
			if err != nil {
				return nil, err
			}
			results := idx.Search(query, limit)
			out := make([]httpapi.KnowledgeSearchResult, len(results))
			for i, r := range results {
				out[i] = httpapi.KnowledgeSearchResult{
					Filename: r.Filename, Snippet: r.Snippet,
					Score: r.Score, LineStart: r.LineStart,
				}
			}
			return out, nil
		},
		QueryReflections: func(dbPath, role string, limit int) ([]httpapi.ReflectionResult, error) {
			refs, err := queryReflections(dbPath, role, limit)
			if err != nil {
				return nil, err
			}
			out := make([]httpapi.ReflectionResult, len(refs))
			for i, r := range refs {
				out[i] = httpapi.ReflectionResult{
					TaskID: r.TaskID, Agent: r.Agent, Score: r.Score,
					Feedback: r.Feedback, Improvement: r.Improvement,
					CostUSD: r.CostUSD, CreatedAt: r.CreatedAt,
				}
			}
			return out, nil
		},
	})
	var pushManager *PushManager
	if cfg.Push.Enabled {
		pushManager = newPushManager(cfg)
	}
	pairingManager := pairing.New(pairing.Config{
		HistoryDB:      cfg.HistoryDB,
		DMPairing:      cfg.AccessControl.DMPairing,
		PairingExpiry:  cfg.AccessControl.PairingExpiry,
		PairingMessage: cfg.AccessControl.PairingMessage,
		Allowlists:     cfg.AccessControl.Allowlists,
	})
	httpapi.RegisterPushRoutes(mux, httpapi.PushDeps{
		Enabled:  cfg.Push.Enabled && pushManager != nil,
		VAPIDKey: cfg.Push.VAPIDPublicKey,
		Subscribe: func(endpoint, p256dh, auth, userAgent string) error {
			return pushManager.Subscribe(PushSubscription{
				Endpoint:  endpoint,
				Keys:      PushKeys{P256dh: p256dh, Auth: auth},
				UserAgent: userAgent,
			})
		},
		Unsubscribe: func(endpoint string) error { return pushManager.Unsubscribe(endpoint) },
		SendTest: func(title, body, icon string) error {
			return pushManager.SendNotification(PushNotification{Title: title, Body: body, Icon: icon})
		},
		ListSubs: func() any {
			subs := pushManager.ListSubscriptions()
			return map[string]any{"subscriptions": subs, "count": len(subs)}
		},
	}, httpapi.PairingDeps{
		ListPending: func() any {
			pending := pairingManager.ListPending()
			return map[string]any{"pending": pending, "count": len(pending)}
		},
		Approve: func(code string) (any, error) {
			approved, err := pairingManager.Approve(code)
			if err != nil {
				return nil, err
			}
			return map[string]any{"status": "approved", "channel": approved.Channel, "userId": approved.UserID}, nil
		},
		Reject:       func(code string) error { return pairingManager.Reject(code) },
		ListApproved: func() (any, error) {
			approved, err := pairingManager.ListApproved()
			if err != nil {
				return nil, err
			}
			return map[string]any{"approved": approved, "count": len(approved)}, nil
		},
		Revoke: func(channel, userID string) error { return pairingManager.Revoke(channel, userID) },
	})
	httpapi.RegisterReminderRoutes(mux, httpapi.ReminderDeps{
		Engine:        s.app.Reminder,
		ParseTime:     parseNaturalTime,
		ParseCronExpr: func(expr string) (any, error) { return parseCronExpr(expr) },
	})
	s.registerAdminRoutes(mux)
	httpapi.RegisterFamilyRoutes(mux, s.app.Family, func() string { return s.Cfg().HistoryDB })
	httpapi.RegisterContactsRoutes(mux, s.app.Contacts, func() string { return s.Cfg().HistoryDB })
	httpapi.RegisterHabitsRoutes(mux, s.app.Habits)
	httpapi.RegisterProjectRoutes(mux, httpapi.ProjectsDeps{
		ListProjects: func(status string) (any, error) {
			projects, err := listProjects(cfg.HistoryDB, status)
			if err != nil {
				return nil, err
			}
			if projects == nil {
				projects = []Project{}
			}
			return map[string]any{"projects": projects, "count": len(projects)}, nil
		},
		GetProject: func(id string) (any, error) { return getProject(cfg.HistoryDB, id) },
		CreateProject: func(raw json.RawMessage) (any, error) {
			var p Project
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, err
			}
			if p.Name == "" {
				return nil, fmt.Errorf("name is required")
			}
			if p.ID == "" {
				p.ID = generateProjectID()
			}
			now := time.Now().UTC().Format(time.RFC3339)
			p.CreatedAt = now
			p.UpdatedAt = now
			if p.Status == "" {
				p.Status = "active"
			}
			return p, createProject(cfg.HistoryDB, p)
		},
		UpdateProject: func(id string, raw json.RawMessage) (any, error) {
			var p Project
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, err
			}
			p.ID = id
			existing, err := getProject(cfg.HistoryDB, id)
			if err != nil {
				return nil, err
			}
			if existing == nil {
				return nil, fmt.Errorf("not found")
			}
			p.CreatedAt = existing.CreatedAt
			if err := updateProject(cfg.HistoryDB, p); err != nil {
				return nil, err
			}
			updated, _ := getProject(cfg.HistoryDB, id)
			if updated != nil {
				return updated, nil
			}
			return p, nil
		},
		DeleteProject: func(id string) error {
			existing, err := getProject(cfg.HistoryDB, id)
			if err != nil {
				return err
			}
			if existing == nil {
				return fmt.Errorf("not found")
			}
			return deleteProject(cfg.HistoryDB, id)
		},
		ScanWorkspace: func() (any, string, error) {
			projectsFile := filepath.Join(cfg.WorkspaceDir, "projects", "PROJECTS.md")
			entries, err := parseProjectsMD(projectsFile)
			return entries, projectsFile, err
		},
		GetProjectStats: func(id string) (any, error) {
			tb := newTaskBoardEngine(cfg.HistoryDB, cfg.TaskBoard, cfg.Webhooks)
			return tb.GetProjectStats(id)
		},
		TaskBoardEnabled: cfg.TaskBoard.Enabled,
		HistoryDB:        func() string { return s.Cfg().HistoryDB },
	})
	s.registerWSEventsRoutes(mux)
	httpapi.RegisterDiscordRoutes(mux, httpapi.DiscordDeps{
		GetNotifications: func() []httpapi.NotifChannel {
			channels := s.Cfg().Notifications
			out := make([]httpapi.NotifChannel, 0, len(channels))
			for _, ch := range channels {
				if ch.Type == "discord" {
					out = append(out, httpapi.NotifChannel{
						Name: ch.Name, Type: ch.Type, WebhookURL: ch.WebhookURL, Events: ch.Events,
					})
				}
			}
			return out
		},
		FindConfigPath:    findConfigPath,
		ChannelSessionKey: channelSessionKey,
		FindSession:       func(dbPath, chKey string) (any, error) { return findChannelSession(dbPath, chKey) },
		HistoryDB:         func() string { return s.Cfg().HistoryDB },
	})
	httpapi.RegisterWorkersRoutes(mux, httpapi.WorkersDeps{
		ListWorkers: func() any {
			type workerInfo struct {
				SessionId    string  `json:"sessionId"`
				Name         string  `json:"name"`
				State        string  `json:"state"`
				Workdir      string  `json:"workdir"`
				Uptime       string  `json:"uptime"`
				ToolCount    int     `json:"toolCount"`
				LastTool     string  `json:"lastTool,omitempty"`
				Source       string  `json:"source"`
				Agent        string  `json:"agent,omitempty"`
				TaskName     string  `json:"taskName,omitempty"`
				TaskID       string  `json:"taskId,omitempty"`
				JobID        string  `json:"jobId,omitempty"`
				CostUSD      float64 `json:"costUsd,omitempty"`
				InputTokens  int     `json:"inputTokens,omitempty"`
				OutputTokens int     `json:"outputTokens,omitempty"`
				ContextPct   int     `json:"contextPct,omitempty"`
				Model        string  `json:"model,omitempty"`
			}
			var out []workerInfo
			if s.hookReceiver != nil {
				for _, hw := range s.hookReceiver.ListHookWorkers() {
					if hw.State == "done" && time.Since(hw.LastSeen) > 2*time.Minute {
						continue
					}
					sessionShort := hw.SessionID
					if len(sessionShort) > 12 {
						sessionShort = sessionShort[:12]
					}
					wi := workerInfo{
						SessionId: sessionShort, Name: "hook-" + sessionShort,
						State: hw.State, Workdir: hw.Cwd,
						Uptime: time.Since(hw.FirstSeen).Round(time.Second).String(),
						ToolCount: hw.ToolCount, LastTool: hw.LastTool, Source: "manual",
						CostUSD: hw.CostUSD, InputTokens: hw.InputTokens,
						OutputTokens: hw.OutputTokens, ContextPct: hw.ContextPct, Model: hw.Model,
					}
					if o := hw.Origin; o != nil {
						wi.Source = o.Source; wi.Agent = o.Agent; wi.TaskName = o.TaskName
						wi.TaskID = o.TaskID; wi.JobID = o.JobID
						if o.TaskName != "" { wi.Name = o.TaskName }
					}
					out = append(out, wi)
				}
			}
			if out == nil { out = []workerInfo{} }
			return map[string]any{"workers": out, "count": len(out)}
		},
		FindWorkerEvents: func(idPrefix string) any {
			if s.hookReceiver == nil {
				return nil
			}
			worker, events := s.hookReceiver.FindHookWorkerByPrefix(idPrefix)
			if worker == nil {
				return nil
			}
			resp := map[string]any{
				"sessionId": idPrefix,
				"state": worker.State, "workdir": worker.Cwd,
				"toolCount": worker.ToolCount, "lastTool": worker.LastTool,
				"uptime": time.Since(worker.FirstSeen).Round(time.Second).String(),
				"costUsd": worker.CostUSD, "inputTokens": worker.InputTokens,
				"outputTokens": worker.OutputTokens, "contextPct": worker.ContextPct,
				"model": worker.Model, "events": events,
			}
			if o := worker.Origin; o != nil {
				resp["source"] = o.Source; resp["agent"] = o.Agent
				resp["taskName"] = o.TaskName; resp["taskId"] = o.TaskID; resp["jobId"] = o.JobID
			} else {
				resp["source"] = "manual"
			}
			return resp
		},
		ListAgentInfos: func() any {
			type agentInfo struct {
				Name     string `json:"name"`
				Provider string `json:"provider"`
				Model    string `json:"model"`
			}
			agents := make([]agentInfo, 0, len(cfg.Agents))
			for name, rc := range cfg.Agents {
				p := rc.Provider
				if p == "" { p = cfg.DefaultProvider }
				if p == "" { p = "claude" }
				agents = append(agents, agentInfo{Name: name, Provider: p, Model: rc.Model})
			}
			return map[string]any{"agents": agents}
		},
		GetDiscordShowProgress: func() bool {
			return s.cfg.Discord.ShowProgress == nil || *s.cfg.Discord.ShowProgress
		},
		SetDiscordShowProgress: func(val bool) {
			s.cfg.Discord.ShowProgress = &val
			configPath := findConfigPath()
			if configPath != "" {
				updateConfigField(configPath, func(raw map[string]any) {
					disc, _ := raw["discord"].(map[string]any)
					if disc == nil { disc = map[string]any{}; raw["discord"] = disc }
					disc["showProgress"] = val
				})
			}
		},
	})
	s.registerHookRoutes(mux)
	s.registerPlanReviewRoutes(mux)
	registerDocsRoutesVia(mux)
	httpapi.RegisterClaudeMCPRoutes(mux)

	// PWA assets.
	mux.HandleFunc("/dashboard/manifest.json", pwa.HandleManifest)
	mux.HandleFunc("/dashboard/sw.js", pwa.HandleServiceWorker(tetoraVersion))
	mux.HandleFunc("/dashboard/icon.svg", pwa.HandleIcon)

	// Locale API.
	mux.HandleFunc("/api/locales/", handleLocaleGet)
	mux.HandleFunc("/api/locales", handleLocalesList)

	// Root redirect to dashboard.
	mux.HandleFunc("/", handleRootRedirect)

	// Dashboard.
	mux.HandleFunc("/dashboard/office-bg.webp", handleOfficeBg)
	mux.HandleFunc("/dashboard/sprites/", handleSprite)
	mux.HandleFunc("/dashboard", handleDashboard)

	// Middleware chain: recovery → trace → body size → rate limit → dashboard auth → IP allowlist → API auth → client ID → mux
	handler := recoveryMiddleware(trace.Middleware(bodySizeMiddleware(rateLimitMiddleware(cfg, s.apiLimiter,
		dashboardAuthMiddleware(cfg,
			ipAllowlistMiddleware(allowlist, cfg.HistoryDB,
				authMiddleware(cfg, s.secMon,
					clientMiddleware(cfg.DefaultClientID, mux))))))))

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

	// Pre-bind the port synchronously before returning. This prevents the
	// daemon from proceeding with Discord/service initialization while the
	// HTTP server goroutine hasn't bound yet — which causes split-brain
	// (Discord bot in one process, HTTP server in another).
	ln, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		log.Error("http server bind failed (another instance running?)", "addr", cfg.ListenAddr, "error", err)
		os.Exit(1)
	}

	// Start with TLS if configured.
	if cfg.TLSEnabled {
		srv.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
		go func() {
			ln.Close() // release pre-bound listener; TLS needs its own
			if err := srv.ListenAndServeTLS(cfg.TLS.CertFile, cfg.TLS.KeyFile); err != http.ErrServerClosed {
				log.Error("https server error", "error", err)
				os.Exit(1)
			}
		}()
		log.Info("https server listening", "addr", cfg.ListenAddr)
	} else {
		go func() {
			if err := srv.Serve(ln); err != http.ErrServerClosed {
				log.Error("http server error", "error", err)
				os.Exit(1)
			}
		}()
		log.Info("http server listening", "addr", cfg.ListenAddr)
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

// --- Security Monitor ---

// securityMonitor tracks security-related events and sends alerts
// when suspicious patterns are detected (e.g. auth failure bursts).
type securityMonitor struct {
	mu            sync.Mutex
	events        map[string][]time.Time // ip -> event timestamps
	lastAlert     map[string]time.Time   // ip -> last alert time (dedup)
	threshold     int                    // number of failures to trigger alert
	windowMin     int                    // window in minutes
	alertCooldown time.Duration          // min time between alerts for same IP
	notifyFn      func(string)           // notification callback
}

func newSecurityMonitor(cfg *Config, notifyFn func(string)) *securityMonitor {
	if !cfg.SecurityAlert.Enabled || notifyFn == nil {
		return nil
	}
	threshold := cfg.SecurityAlert.FailThreshold
	if threshold <= 0 {
		threshold = 10
	}
	windowMin := cfg.SecurityAlert.FailWindowMin
	if windowMin <= 0 {
		windowMin = 5
	}
	return &securityMonitor{
		events:        make(map[string][]time.Time),
		lastAlert:     make(map[string]time.Time),
		threshold:     threshold,
		windowMin:     windowMin,
		alertCooldown: 15 * time.Minute,
		notifyFn:      notifyFn,
	}
}

// recordEvent records a security event for the given IP.
// If the event count exceeds the threshold within the window, an alert is sent.
func (sm *securityMonitor) recordEvent(ip, eventType string) {
	if sm == nil {
		return
	}

	sm.mu.Lock()

	now := time.Now()
	cutoff := now.Add(-time.Duration(sm.windowMin) * time.Minute)

	key := ip

	// Get or create event list.
	events := sm.events[key]

	// Trim old events outside window.
	start := 0
	for start < len(events) && events[start].Before(cutoff) {
		start++
	}
	events = events[start:]

	// Add new event.
	events = append(events, now)
	sm.events[key] = events

	// Check threshold.
	var alertMsg string
	if len(events) >= sm.threshold {
		// Dedup: don't alert same IP more than once per cooldown.
		if last, ok := sm.lastAlert[key]; !ok || now.Sub(last) >= sm.alertCooldown {
			sm.lastAlert[key] = now
			alertMsg = fmt.Sprintf("[Security] Suspicious activity from %s: %d %s events in %dm",
				ip, len(events), eventType, sm.windowMin)
		}
	}
	sm.mu.Unlock()

	// Send notification outside of the lock to avoid holding it during I/O.
	if alertMsg != "" {
		sm.notifyFn(alertMsg)
	}
}

// cleanup removes expired entries to prevent memory leak.
func (sm *securityMonitor) cleanup() {
	if sm == nil {
		return
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	cutoff := time.Now().Add(-time.Duration(sm.windowMin) * time.Minute)
	for ip, events := range sm.events {
		if len(events) == 0 || events[len(events)-1].Before(cutoff) {
			delete(sm.events, ip)
		}
	}

	// Clean up old alert dedup entries.
	alertCutoff := time.Now().Add(-sm.alertCooldown)
	for ip, last := range sm.lastAlert {
		if last.Before(alertCutoff) {
			delete(sm.lastAlert, ip)
		}
	}
}

// wsEventsHub manages WebSocket connections that mirror the SSE dashboard feed.
type wsEventsHub struct {
	mu      sync.RWMutex
	clients map[net.Conn]struct{}
}

func newWSEventsHub() *wsEventsHub {
	return &wsEventsHub{
		clients: make(map[net.Conn]struct{}),
	}
}

// add registers a new WebSocket client connection.
func (h *wsEventsHub) add(conn net.Conn) {
	h.mu.Lock()
	h.clients[conn] = struct{}{}
	h.mu.Unlock()
}

// remove unregisters and closes a WebSocket client connection.
func (h *wsEventsHub) remove(conn net.Conn) {
	h.mu.Lock()
	delete(h.clients, conn)
	h.mu.Unlock()
	conn.Close()
}

// broadcast sends a JSON-encoded event to all connected WebSocket clients.
// Clients that fail to receive are removed.
func (h *wsEventsHub) broadcast(event SSEEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}

	h.mu.RLock()
	conns := make([]net.Conn, 0, len(h.clients))
	for conn := range h.clients {
		conns = append(conns, conn)
	}
	h.mu.RUnlock()

	var failed []net.Conn
	for _, conn := range conns {
		if err := relayWSWriteMessage(conn, data); err != nil {
			failed = append(failed, conn)
		}
	}
	for _, conn := range failed {
		h.remove(conn)
	}
}

// global hub instance — initialized in registerWSEventsRoutes.
var globalWSEventsHub *wsEventsHub

func (s *Server) registerWSEventsRoutes(mux *http.ServeMux) {
	state := s.state

	hub := newWSEventsHub()
	globalWSEventsHub = hub

	// Start a goroutine that subscribes to the SSE dashboard broker and forwards
	// events to all connected WebSocket clients.
	if state != nil && state.broker != nil {
		broker := state.broker
		go func() {
			// Subscribe to the global dashboard feed. This channel lives for the
			// duration of the process — the unsubscribe is called if this goroutine
			// ever exits (shouldn't happen in normal operation).
			ch, unsub := broker.Subscribe(SSEDashboardKey)
			defer unsub()
			for event := range ch {
				hub.broadcast(event)
			}
		}()
	}

	// GET /ws/events — WebSocket upgrade endpoint.
	mux.HandleFunc("/ws/events", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}

		if r.Header.Get("Upgrade") != "websocket" {
			http.Error(w, `{"error":"websocket upgrade required"}`, http.StatusBadRequest)
			return
		}

		key := r.Header.Get("Sec-WebSocket-Key")
		if key == "" {
			http.Error(w, `{"error":"missing Sec-WebSocket-Key"}`, http.StatusBadRequest)
			return
		}

		acceptKey := computeWebSocketAccept(key)

		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, `{"error":"hijack not supported"}`, http.StatusInternalServerError)
			return
		}
		conn, bufrw, err := hj.Hijack()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Send WebSocket upgrade response.
		bufrw.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
		bufrw.WriteString("Upgrade: websocket\r\n")
		bufrw.WriteString("Connection: Upgrade\r\n")
		bufrw.WriteString("Sec-WebSocket-Accept: " + acceptKey + "\r\n\r\n")
		bufrw.Flush()

		hub.add(conn)
		log.Info("ws/events client connected", "remote", conn.RemoteAddr().String())

		// Read loop: drain incoming frames until client disconnects.
		// This is a server-push only endpoint; client messages are discarded.
		wsEventsReadLoop(conn)

		hub.remove(conn)
		log.Info("ws/events client disconnected")
	})
}

// wsEventsReadLoop reads and discards frames from the client until disconnect.
func wsEventsReadLoop(conn net.Conn) {
	for {
		_, err := relayWSReadMessage(conn)
		if err != nil {
			return
		}
	}
}

//go:embed dashboard.html
var dashboardHTML []byte

//go:embed assets/office_bg.webp
var officeBgWebp []byte

//go:embed assets/sprites/sprite_ruri.png assets/sprites/sprite_hisui.png assets/sprites/sprite_kokuyou.png assets/sprites/sprite_kohaku.png assets/sprites/sprite_default.png
var spriteFS embed.FS

//go:embed assets/portraits
var portraitFS embed.FS

//go:embed README.md INSTALL.md CHANGELOG.md ROADMAP.md CONTRIBUTING.md docs/*.md docs/i18n/*.md
var docsFS embed.FS

var supportedDocsLangs = []string{"zh-TW", "ja", "ko", "id", "th", "fil", "es", "fr", "de"}

var docsList = []httpapi.DocsPageEntry{
	{Name: "README", File: "README.md", Description: "Project Overview"},
	{Name: "Configuration", File: "docs/configuration.md", Description: "Config Reference"},
	{Name: "Workflows", File: "docs/workflow.md", Description: "Workflow Engine"},
	{Name: "Taskboard", File: "docs/taskboard.md", Description: "Kanban & Auto-Dispatch"},
	{Name: "Hooks", File: "docs/hooks.md", Description: "Claude Code Hooks"},
	{Name: "MCP", File: "docs/mcp.md", Description: "Model Context Protocol"},
	{Name: "Discord Commands", File: "docs/discord-commands.md", Description: "Chat Commands & Model Switching"},
	{Name: "Discord Multitasking", File: "docs/discord-multitasking.md", Description: "Thread & Focus"},
	{Name: "Troubleshooting", File: "docs/troubleshooting.md", Description: "Common Issues"},
	{Name: "Changelog", File: "CHANGELOG.md", Description: "Release History"},
	{Name: "Roadmap", File: "ROADMAP.md", Description: "Future Plans"},
	{Name: "Contributing", File: "CONTRIBUTING.md", Description: "Contributor Guide"},
	{Name: "Installation", File: "INSTALL.md", Description: "Setup Guide"},
}

func handleRootRedirect(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		http.Redirect(w, r, "/dashboard", http.StatusFound)
		return
	}
	http.NotFound(w, r)
}

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
	data, err := spriteFS.ReadFile("assets/sprites/sprite_" + name)
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

// registerDocsRoutesVia delegates to httpapi.RegisterDocsRoutes with embedded FS.
func registerDocsRoutesVia(mux *http.ServeMux) {
	httpapi.RegisterDocsRoutes(mux, docsFS, docsList, supportedDocsLangs)
}

// --- Sprite State Constants ---

const (
	SpriteIdle      = sprite.Idle
	SpriteWork      = sprite.Work
	SpriteThink     = sprite.Think
	SpriteTalk      = sprite.Talk
	SpriteReview    = sprite.Review
	SpriteCelebrate = sprite.Celebrate
	SpriteError     = sprite.Error

	SpriteWalkDown  = sprite.WalkDown
	SpriteWalkUp    = sprite.WalkUp
	SpriteWalkLeft  = sprite.WalkLeft
	SpriteWalkRight = sprite.WalkRight
)

// --- Type Aliases ---

type SpriteStateDef = sprite.StateDef
type AgentSpriteDef = sprite.AgentDef
type agentSpriteTracker = sprite.Tracker

// --- Wrapper Functions ---

func defaultSpriteConfig() SpriteConfig                              { return sprite.DefaultConfig() }
func loadSpriteConfig(dir string, keys []string) SpriteConfig        { return sprite.LoadConfig(dir, keys) }
func initSpriteConfig(dir string) error                              { return sprite.InitConfig(dir) }
func newAgentSpriteTracker() *agentSpriteTracker                     { return sprite.NewTracker() }

// resolvePortraitURL returns the URL for an agent's portrait.
// Priority: user-uploaded custom (~/.tetora/media/portraits/{name}.png)
//           > built-in embedded (assets/portraits/{name}.png)
//           > empty string (frontend shows CSS fallback)
func resolvePortraitURL(baseDir, name string) string {
	custom := filepath.Join(baseDir, "media", "portraits", name+".png")
	if _, err := os.Stat(custom); err == nil {
		return "/media/portraits/" + name + ".png"
	}
	if _, err := portraitFS.Open("assets/portraits/" + name + ".png"); err == nil {
		return "/dashboard/portraits/" + name + ".png"
	}
	// fallback to default built-in
	if _, err := portraitFS.Open("assets/portraits/default.png"); err == nil {
		return "/dashboard/portraits/default.png"
	}
	return ""
}

// --- State Resolution ---

// isChatSource returns true if the task source indicates a chat conversation.
func isChatSource(source string) bool {
	s := strings.ToLower(source)
	if i := strings.IndexByte(s, ':'); i > 0 {
		s = s[:i]
	}
	return classify.ChatSources[s]
}

// resolveAgentSprite determines the sprite state from dispatch/task context.
func resolveAgentSprite(taskStatus, dispatchStatus, source string) string {
	switch taskStatus {
	case "failed", "error":
		return SpriteError
	case "done", "success":
		return SpriteCelebrate
	case "review":
		return SpriteReview
	}

	if isChatSource(source) && (taskStatus == "running" || taskStatus == "doing") {
		return SpriteTalk
	}

	switch dispatchStatus {
	case "dispatching":
		return SpriteThink
	}

	switch taskStatus {
	case "running", "doing", "processing":
		return SpriteWork
	}

	return SpriteIdle
}

func (s *Server) registerAdminRoutes(mux *http.ServeMux) {
	cfg := s.cfg
	state := s.state
	sem := s.sem
	childSem := s.childSem

	// --- Incoming Webhooks ---
	mux.HandleFunc("/hooks/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/hooks/")
		if name == "" {
			http.Error(w, `{"error":"webhook name required"}`, http.StatusBadRequest)
			return
		}
		ctx := r.Context()
		result := handleIncomingWebhook(ctx, cfg, name, r, state, sem, childSem)
		w.Header().Set("Content-Type", "application/json")
		switch result.Status {
		case "error":
			w.WriteHeader(http.StatusBadRequest)
		case "disabled":
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusOK)
		}
		json.NewEncoder(w).Encode(result)
	})

	mux.HandleFunc("/webhooks/incoming", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		type webhookInfo struct {
			Name      string `json:"name"`
			Agent      string `json:"agent"`
			Enabled   bool   `json:"enabled"`
			Template  string `json:"template,omitempty"`
			Filter    string `json:"filter,omitempty"`
			Workflow  string `json:"workflow,omitempty"`
			HasSecret bool   `json:"hasSecret"`
		}
		var list []webhookInfo
		for name, wh := range cfg.IncomingWebhooks {
			list = append(list, webhookInfo{
				Name:      name,
				Agent:      wh.Agent,
				Enabled:   wh.IsEnabled(),
				Template:  wh.Template,
				Filter:    wh.Filter,
				Workflow:  wh.Workflow,
				HasSecret: wh.Secret != "",
			})
		}
		if list == nil {
			list = []webhookInfo{}
		}
		json.NewEncoder(w).Encode(list)
	})

	mux.HandleFunc("/audit", func(w http.ResponseWriter, r *http.Request) {
		if cfg.HistoryDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		limit := 50
		offset := 0
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 {
				limit = n
			}
		}
		if p := r.URL.Query().Get("page"); p != "" {
			if n, err := strconv.Atoi(p); err == nil && n > 1 {
				offset = (n - 1) * limit
			}
		}

		entries, total, err := audit.Query(cfg.HistoryDB, limit, offset)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if entries == nil {
			entries = []audit.Entry{}
		}

		page := (offset / limit) + 1
		json.NewEncoder(w).Encode(map[string]any{
			"entries": entries,
			"total":   total,
			"page":    page,
			"limit":   limit,
		})
	})

	// --- Retention & Data ---
	mux.HandleFunc("/retention", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			stats := make(map[string]int)
			if cfg.HistoryDB != "" {
				stats = queryRetentionStats(cfg.HistoryDB)
			}
			json.NewEncoder(w).Encode(map[string]any{
				"config": cfg.Retention,
				"defaults": map[string]int{
					"history":     retentionDays(cfg.Retention.History, 90),
					"sessions":    retentionDays(cfg.Retention.Sessions, 30),
					"auditLog":    retentionDays(cfg.Retention.AuditLog, 365),
					"logs":        retentionDays(cfg.Retention.Logs, 14),
					"workflows":   retentionDays(cfg.Retention.Workflows, 90),
					"reflections": retentionDays(cfg.Retention.Reflections, 60),
					"sla":         retentionDays(cfg.Retention.SLA, 90),
					"trustEvents": retentionDays(cfg.Retention.TrustEvents, 90),
					"handoffs":    retentionDays(cfg.Retention.Handoffs, 60),
					"queue":       retentionDays(cfg.Retention.Queue, 7),
					"versions":    retentionDays(cfg.Retention.Versions, 180),
					"outputs":     retentionDays(cfg.Retention.Outputs, 30),
					"uploads":     retentionDays(cfg.Retention.Uploads, 7),
				},
				"stats": stats,
			})
		default:
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/retention/cleanup", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		audit.Log(cfg.HistoryDB, "retention.cleanup", "http", "", clientIP(r))
		results := runRetention(cfg)
		json.NewEncoder(w).Encode(map[string]any{"results": results})
	})

	mux.HandleFunc("/data/export", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		audit.Log(cfg.HistoryDB, "data.export", "http", "", clientIP(r))
		data, err := exportData(cfg)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Write(data)
	})

	mux.HandleFunc("/data/purge", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, `{"error":"DELETE only"}`, http.StatusMethodNotAllowed)
			return
		}
		before := r.URL.Query().Get("before")
		if before == "" {
			http.Error(w, `{"error":"before parameter required (YYYY-MM-DD)"}`, http.StatusBadRequest)
			return
		}
		confirm := r.Header.Get("X-Confirm-Purge")
		if confirm != "true" {
			http.Error(w, `{"error":"X-Confirm-Purge: true header required"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		audit.Log(cfg.HistoryDB, "data.purge", "http", "before="+before, clientIP(r))
		results, err := purgeDataBefore(cfg, before)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"results": results})
	})

	// --- Backup ---
	mux.HandleFunc("/backup", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}

		audit.Log(cfg.HistoryDB, "backup.download", "http", "", clientIP(r))

		// Create temp backup.
		tmpFile, err := os.CreateTemp("", "tetora-backup-*.tar.gz")
		if err != nil {
			jsonError(w, "create temp: "+err.Error(), http.StatusInternalServerError)
			return
		}
		tmpPath := tmpFile.Name()
		tmpFile.Close()
		defer os.Remove(tmpPath)

		if err := backup.Create(cfg.BaseDir, tmpPath); err != nil {
			jsonError(w, "create backup: "+err.Error(), http.StatusInternalServerError)
			return
		}

		data, err := os.ReadFile(tmpPath)
		if err != nil {
			jsonError(w, "read backup: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", "attachment; filename=tetora-backup.tar.gz")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Write(data)
	})

	// Dashboard login.
	mux.HandleFunc("/dashboard/login", func(w http.ResponseWriter, r *http.Request) {
		if !cfg.DashboardAuth.Enabled {
			http.Redirect(w, r, "/dashboard", http.StatusFound)
			return
		}

		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(dashboardLoginHTML))
			return
		}

		if r.Method == http.MethodPost {
			ip := clientIP(r)

			// Rate limit check.
			if s.limiter.isLocked(ip) {
				audit.Log(cfg.HistoryDB, "dashboard.login.ratelimit", "http", "", ip)
				if s.secMon != nil {
					s.secMon.recordEvent(ip, "login.ratelimit")
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(dashboardLoginLockedHTML))
				return
			}

			r.ParseForm()
			password := r.FormValue("password")

			expected := cfg.DashboardAuth.Password
			if expected == "" {
				expected = cfg.DashboardAuth.Token
			}

			if password != expected {
				s.limiter.recordFailure(ip)
				audit.Log(cfg.HistoryDB, "dashboard.login.fail", "http", "", ip)
				if s.secMon != nil {
					s.secMon.recordEvent(ip, "login.fail")
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(dashboardLoginFailHTML))
				return
			}

			// Success — clear rate limit.
			s.limiter.recordSuccess(ip)

			// Set session cookie.
			secret := expected
			cookieVal := dashboardAuthCookie(secret)
			cookie := &http.Cookie{
				Name:     "tetora_session",
				Value:    cookieVal,
				Path:     "/dashboard",
				MaxAge:   86400, // 24h
				HttpOnly: true,
				SameSite: http.SameSiteStrictMode,
			}
			if cfg.TLSEnabled {
				cookie.Secure = true
			}
			http.SetCookie(w, cookie)
			audit.Log(cfg.HistoryDB, "dashboard.login", "http", "", ip)
			http.Redirect(w, r, "/dashboard", http.StatusFound)
			return
		}

		http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
	})

	// --- Config & Workflow Versioning ---
	mux.HandleFunc("/config/versions", func(w http.ResponseWriter, r *http.Request) {
		if cfg.HistoryDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			limit := 20
			if l := r.URL.Query().Get("limit"); l != "" {
				if n, err := strconv.Atoi(l); err == nil && n > 0 {
					limit = n
				}
			}
			versions, err := version.QueryVersions(cfg.HistoryDB, "config", "config.json", limit)
			if err != nil {
				jsonError(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if versions == nil {
				versions = []ConfigVersion{}
			}
			json.NewEncoder(w).Encode(versions)
		case http.MethodPost:
			// Manual snapshot.
			var req struct {
				Reason string `json:"reason"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			configPath := filepath.Join(cfg.BaseDir, "config.json")
			if err := version.SnapshotConfig(cfg.HistoryDB, configPath, "api", req.Reason); err != nil {
				jsonError(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"status":"ok"}`))
		default:
			http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/config/versions/", func(w http.ResponseWriter, r *http.Request) {
		if cfg.HistoryDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		path := strings.TrimPrefix(r.URL.Path, "/config/versions/")

		// GET /config/versions/{id} — show version detail
		if r.Method == http.MethodGet && !strings.Contains(path, "/") {
			ver, err := version.QueryByID(cfg.HistoryDB, path)
			if err != nil {
				jsonError(w, err.Error(), http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(ver)
			return
		}

		// POST /config/versions/{id}/restore
		if r.Method == http.MethodPost && strings.HasSuffix(path, "/restore") {
			versionID := strings.TrimSuffix(path, "/restore")
			configPath := filepath.Join(cfg.BaseDir, "config.json")
			if _, err := version.RestoreConfig(cfg.HistoryDB, configPath, versionID); err != nil {
				jsonError(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.Write([]byte(`{"status":"restored","note":"restart daemon for changes to take effect"}`))
			return
		}

		// GET /config/versions/{id}/diff/{id2}
		if r.Method == http.MethodGet && strings.Contains(path, "/diff/") {
			parts := strings.SplitN(path, "/diff/", 2)
			if len(parts) != 2 {
				http.Error(w, `{"error":"use GET /config/versions/{id}/diff/{id2}"}`, http.StatusBadRequest)
				return
			}
			result, err := version.DiffDetail(cfg.HistoryDB, parts[0], parts[1])
			if err != nil {
				jsonError(w, err.Error(), http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(result)
			return
		}

		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
	})

	mux.HandleFunc("/versions", func(w http.ResponseWriter, r *http.Request) {
		if cfg.HistoryDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		entityType := r.URL.Query().Get("type")
		entityName := r.URL.Query().Get("name")
		limit := 20
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 {
				limit = n
			}
		}
		if entityType == "" {
			// List all versioned entities.
			entities, err := version.QueryAllEntities(cfg.HistoryDB)
			if err != nil {
				jsonError(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if entities == nil {
				entities = []ConfigVersion{}
			}
			json.NewEncoder(w).Encode(entities)
			return
		}
		versions, err := version.QueryVersions(cfg.HistoryDB, entityType, entityName, limit)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if versions == nil {
			versions = []ConfigVersion{}
		}
		json.NewEncoder(w).Encode(versions)
	})

	// --- P13.1: Plugin System --- Plugin API routes.
	mux.HandleFunc("/api/plugins", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if s.pluginHost == nil {
			json.NewEncoder(w).Encode([]any{})
			return
		}
		json.NewEncoder(w).Encode(s.pluginHost.List())
	})

	mux.HandleFunc("/api/plugins/", func(w http.ResponseWriter, r *http.Request) {
		// Parse /api/plugins/{name}/{action}
		path := strings.TrimPrefix(r.URL.Path, "/api/plugins/")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) == 0 || parts[0] == "" {
			http.Error(w, `{"error":"plugin name required"}`, http.StatusBadRequest)
			return
		}
		name := parts[0]
		action := ""
		if len(parts) > 1 {
			action = parts[1]
		}

		w.Header().Set("Content-Type", "application/json")

		if s.pluginHost == nil {
			http.Error(w, `{"error":"plugin system not initialized"}`, http.StatusServiceUnavailable)
			return
		}

		switch action {
		case "start":
			if r.Method != http.MethodPost {
				http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
				return
			}
			if err := s.pluginHost.Start(name); err != nil {
				jsonError(w, err.Error(), http.StatusBadRequest)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"status": "started", "name": name})

		case "stop":
			if r.Method != http.MethodPost {
				http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
				return
			}
			if err := s.pluginHost.Stop(name); err != nil {
				jsonError(w, err.Error(), http.StatusBadRequest)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"status": "stopped", "name": name})

		case "health":
			if r.Method != http.MethodGet {
				http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
				return
			}
			json.NewEncoder(w).Encode(s.pluginHost.Health(name))

		default:
			http.Error(w, `{"error":"unknown action, use start, stop, or health"}`, http.StatusBadRequest)
		}
	})

	// --- P18.4: Self-Improving Skills Store API ---
	mux.HandleFunc("/api/skills/store", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			allMetas := loadAllFileSkillMetas(cfg)
			pending := listPendingSkills(cfg)
			json.NewEncoder(w).Encode(map[string]any{
				"skills":  allMetas,
				"pending": pending,
				"total":   len(allMetas),
			})
		default:
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/skills/store/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Parse /api/skills/store/<name>/<action>
		path := strings.TrimPrefix(r.URL.Path, "/api/skills/store/")
		if path == "" {
			http.Error(w, `{"error":"name required"}`, http.StatusBadRequest)
			return
		}
		parts := strings.SplitN(path, "/", 2)
		name := parts[0]
		action := ""
		if len(parts) > 1 {
			action = parts[1]
		}

		switch r.Method {
		case http.MethodPost:
			switch action {
			case "approve":
				audit.Log(cfg.HistoryDB, "skill.approve", "http",
					fmt.Sprintf("name=%s", name), clientIP(r))
				if err := approveSkill(cfg, name); err != nil {
					jsonError(w, err.Error(), http.StatusBadRequest)
					return
				}
				if cfg.HistoryDB != "" {
					recordSkillEvent(cfg.HistoryDB, name, "approved", "", "http")
				}
				json.NewEncoder(w).Encode(map[string]string{"status": "approved", "name": name})

			case "reject":
				audit.Log(cfg.HistoryDB, "skill.reject", "http",
					fmt.Sprintf("name=%s", name), clientIP(r))
				if err := rejectSkill(cfg, name); err != nil {
					jsonError(w, err.Error(), http.StatusBadRequest)
					return
				}
				if cfg.HistoryDB != "" {
					recordSkillEvent(cfg.HistoryDB, name, "rejected", "", "http")
				}
				json.NewEncoder(w).Encode(map[string]string{"status": "rejected", "name": name})

			default:
				http.Error(w, `{"error":"unknown action, use approve or reject"}`, http.StatusBadRequest)
			}

		case http.MethodDelete:
			if action != "" {
				http.Error(w, `{"error":"DELETE /api/skills/store/<name>"}`, http.StatusBadRequest)
				return
			}
			audit.Log(cfg.HistoryDB, "skill.delete", "http",
				fmt.Sprintf("name=%s", name), clientIP(r))
			if err := deleteFileSkill(cfg, name); err != nil {
				jsonError(w, err.Error(), http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"status": "deleted", "name": name})

		default:
			http.Error(w, `{"error":"POST or DELETE only"}`, http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/skills/usage", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		limit := 50
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 200 {
				limit = n
			}
		}

		rows, err := db.Query(cfg.HistoryDB,
			fmt.Sprintf(`SELECT id, skill_name, event_type, task_prompt, role, created_at, status, duration_ms, source, session_id, error_msg FROM skill_usage ORDER BY id DESC LIMIT %d`, limit))
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"events": rows,
			"count":  len(rows),
		})
	})

	// --- P22.4: Integration Status ---
	mux.HandleFunc("/api/integrations/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		type channelStatus struct {
			Name    string `json:"name"`
			Enabled bool   `json:"enabled"`
			Status  string `json:"status"` // "connected", "not_configured", "error"
		}
		type integrationStatus struct {
			Channels      []channelStatus    `json:"channels"`
			OAuthServices []map[string]any   `json:"oauthServices,omitempty"`
			RemindersActive int              `json:"remindersActive"`
			TriggersActive  int              `json:"triggersActive"`
			KnowledgeDocs   int              `json:"knowledgeDocs"`
			BrowserRelay    string           `json:"browserRelay"`    // "connected", "not_configured", "no_clients"
			HomeAssistant   string           `json:"homeAssistant"`   // "connected", "not_configured"
		}

		status := integrationStatus{}

		// Channel statuses.
		channels := []struct {
			name    string
			enabled bool
			bot     any
		}{
			{"telegram", cfg.Telegram.Enabled && cfg.Telegram.BotToken != "", nil},
			{"slack", cfg.Slack.Enabled, s.slackBot},
			{"discord", cfg.Discord.Enabled, nil},
			{"whatsapp", cfg.WhatsApp.Enabled, s.whatsappBot},
			{"line", cfg.LINE.Enabled, s.lineBot},
			{"teams", cfg.Teams.Enabled, s.teamsBot},
			{"signal", cfg.Signal.Enabled, s.signalBot},
			{"gchat", cfg.GoogleChat.Enabled, s.gchatBot},
			{"imessage", cfg.IMessage.Enabled, s.imessageBot},
		}
		for _, ch := range channels {
			cs := channelStatus{Name: ch.name, Enabled: ch.enabled}
			if !ch.enabled {
				cs.Status = "not_configured"
			} else if ch.bot != nil {
				cs.Status = "connected"
			} else {
				cs.Status = "connected" // enabled = assumed connected for webhook-based channels
			}
			status.Channels = append(status.Channels, cs)
		}

		// OAuth services — use listOAuthTokenStatuses directly.
		if cfg.HistoryDB != "" {
			statuses, err := listOAuthTokenStatuses(cfg.HistoryDB, cfg.OAuth.EncryptionKey)
			if err == nil {
				for _, st := range statuses {
					svcMap := map[string]any{
						"name":      st.ServiceName,
						"connected": st.Connected,
					}
					if st.Scopes != "" {
						svcMap["scopes"] = st.Scopes
					}
					status.OAuthServices = append(status.OAuthServices, svcMap)
				}
			}
		}

		// Reminders count — query DB directly.
		if cfg.HistoryDB != "" {
			rows, err := db.Query(cfg.HistoryDB, "SELECT COUNT(*) as cnt FROM reminders WHERE status='pending'")
			if err == nil && len(rows) > 0 {
				if cnt, ok := rows[0]["cnt"]; ok {
					switch v := cnt.(type) {
					case float64:
						status.RemindersActive = int(v)
					case string:
						fmt.Sscanf(v, "%d", &status.RemindersActive)
					}
				}
			}
		}

		// Triggers count.
		triggers := cfg.WorkflowTriggers
		activeCount := 0
		for _, t := range triggers {
			if t.IsEnabled() {
				activeCount++
			}
		}
		status.TriggersActive = activeCount

		// Knowledge docs count.
		if cfg.HistoryDB != "" {
			rows, err := db.Query(cfg.HistoryDB, "SELECT COUNT(*) as cnt FROM knowledge_docs")
			if err == nil && len(rows) > 0 {
				if cnt, ok := rows[0]["cnt"]; ok {
					switch v := cnt.(type) {
					case float64:
						status.KnowledgeDocs = int(v)
					case string:
						fmt.Sscanf(v, "%d", &status.KnowledgeDocs)
					}
				}
			}
		}

		// Browser relay status.
		if !cfg.BrowserRelay.Enabled {
			status.BrowserRelay = "not_configured"
		} else if s.app != nil && s.app.Browser != nil && s.app.Browser.Connected() {
			status.BrowserRelay = "connected"
		} else {
			status.BrowserRelay = "no_clients"
		}

		// Home Assistant status.
		if !cfg.HomeAssistant.Enabled {
			status.HomeAssistant = "not_configured"
		} else if s.app != nil && s.app.HA != nil {
			status.HomeAssistant = "connected"
		} else {
			status.HomeAssistant = "not_configured"
		}

		json.NewEncoder(w).Encode(status)
	})

	// --- P22.5: Config Summary (sanitized) ---
	mux.HandleFunc("/api/config/summary", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		cfg := s.Cfg() // Read latest config (may have been reloaded via SIGHUP).

		maskSecret := func(s string) string {
			if s == "" {
				return "not set"
			}
			return "***configured***"
		}

		toolCount := 0
		if cfg.Runtime.ToolRegistry != nil {
			toolCount = len(cfg.Runtime.ToolRegistry.(*ToolRegistry).List())
		}

		summary := map[string]any{
			"general": map[string]any{
				"listenAddr":     cfg.ListenAddr,
				"maxConcurrent":  cfg.MaxConcurrent,
				"defaultModel":   cfg.DefaultModel,
				"defaultTimeout": cfg.DefaultTimeout,
				"apiToken":       maskSecret(cfg.APIToken),
				"tlsEnabled":     cfg.TLSEnabled,
			},
			"channels": map[string]any{
				"telegram":  cfg.Telegram.Enabled,
				"slack":     cfg.Slack.Enabled,
				"discord":   cfg.Discord.Enabled,
				"whatsapp":  cfg.WhatsApp.Enabled,
				"line":      cfg.LINE.Enabled,
				"teams":     cfg.Teams.Enabled,
				"signal":    cfg.Signal.Enabled,
				"gchat":     cfg.GoogleChat.Enabled,
				"imessage":  cfg.IMessage.Enabled,
			},
			"integrations": map[string]any{
				"weather":       cfg.Weather.Enabled,
				"currency":      cfg.Currency.Enabled,
				"rss":           cfg.RSS.Enabled,
				"translate":     map[string]any{"enabled": cfg.Translate.Enabled, "provider": cfg.Translate.Provider, "apiKey": maskSecret(cfg.Translate.APIKey)},
				"imageGen":      map[string]any{"enabled": cfg.ImageGen.Enabled, "apiKey": maskSecret(cfg.ImageGen.APIKey), "model": cfg.ImageGen.Model, "dailyLimit": cfg.ImageGen.DailyLimit, "maxCostDay": cfg.ImageGen.MaxCostDay},
				"homeAssistant": map[string]any{"enabled": cfg.HomeAssistant.Enabled, "url": cfg.HomeAssistant.BaseURL},
				"gmail":         cfg.Gmail.Enabled,
				"calendar":      cfg.Calendar.Enabled,
				"twitter":       cfg.Twitter.Enabled,
				"browserRelay":  cfg.BrowserRelay.Enabled,
				"notebookLM":    cfg.NotebookLM.Enabled,
			},
			"tools": map[string]any{
				"totalRegistered": toolCount,
			},
			"budgets": map[string]any{
				"dailyLimit":  cfg.CostAlert.DailyLimit,
				"weeklyLimit": cfg.CostAlert.WeeklyLimit,
				"action":      cfg.CostAlert.Action,
			},
			"security": map[string]any{
				"tlsEnabled":    cfg.TLSEnabled,
				"rateLimit":     cfg.RateLimit.Enabled,
				"rateLimitMax":  cfg.RateLimit.MaxPerMin,
				"ipAllowlist":   len(cfg.AllowedIPs),
				"dashboardAuth": cfg.DashboardAuth.Enabled,
			},
			"taskBoard": map[string]any{
				"enabled":         cfg.TaskBoard.Enabled,
				"autoDispatch":    cfg.TaskBoard.AutoDispatch.Enabled,
				"maxRetries":      cfg.TaskBoard.MaxRetries,
				"defaultWorkflow": cfg.TaskBoard.DefaultWorkflow,
			},
			"heartbeat": map[string]any{
				"enabled":          cfg.Heartbeat.Enabled,
				"interval":         cfg.Heartbeat.IntervalOrDefault().String(),
				"stallThreshold":   cfg.Heartbeat.StallThresholdOrDefault().String(),
				"timeoutWarnRatio": cfg.Heartbeat.TimeoutWarnRatioOrDefault(),
				"autoCancel":       cfg.Heartbeat.AutoCancel,
				"notifyOnStall":    cfg.Heartbeat.NotifyOnStallOrDefault(),
			},
		}

		json.NewEncoder(w).Encode(summary)
	})

	// Toggle a config boolean via PATCH /api/config/toggle.
	mux.HandleFunc("/api/config/toggle", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			http.Error(w, `{"error":"PATCH only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		var req struct {
			Key   string `json:"key"`
			Value any    `json:"value"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Whitelist of settable keys: "bool" for toggles, "string" for text fields.
		allowed := map[string]string{
			"taskBoard.enabled":              "bool",
			"taskBoard.autoDispatch.enabled":  "bool",
			"taskBoard.defaultWorkflow":       "string",
			"heartbeat.enabled":              "bool",
			"heartbeat.autoCancel":           "bool",
			"heartbeat.notifyOnStall":        "bool",
		}
		kind, ok := allowed[req.Key]
		if !ok {
			jsonError(w, fmt.Sprintf("key %q not settable", req.Key), http.StatusBadRequest)
			return
		}
		// Type-check the value.
		switch kind {
		case "bool":
			if _, ok := req.Value.(bool); !ok {
				jsonError(w, fmt.Sprintf("key %q requires bool value", req.Key), http.StatusBadRequest)
				return
			}
		case "string":
			if _, ok := req.Value.(string); !ok {
				jsonError(w, fmt.Sprintf("key %q requires string value", req.Key), http.StatusBadRequest)
				return
			}
		}

		configPath := findConfigPath()
		data, err := os.ReadFile(configPath)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Navigate dot path and set value.
		parts := strings.Split(req.Key, ".")
		target := raw
		for i := 0; i < len(parts)-1; i++ {
			sub, ok := target[parts[i]]
			if !ok {
				newMap := make(map[string]any)
				target[parts[i]] = newMap
				target = newMap
				continue
			}
			subMap, ok := sub.(map[string]any)
			if !ok {
				jsonError(w, fmt.Sprintf("cannot traverse %q", req.Key), http.StatusBadRequest)
				return
			}
			target = subMap
		}
		target[parts[len(parts)-1]] = req.Value

		out, err := json.MarshalIndent(raw, "", "  ")
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := os.WriteFile(configPath, append(out, '\n'), 0o600); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Reload config in-memory via SIGHUP.
		signalSelfReload()

		audit.Log(cfg.HistoryDB, "config.toggle", "dashboard",
			fmt.Sprintf("%s=%v", req.Key, req.Value), "")

		respVal, err := json.Marshal(req.Value)
		if err != nil {
			jsonError(w, "marshal: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Write([]byte(fmt.Sprintf(`{"status":"ok","key":"%s","value":%s}`, req.Key, respVal)))
	})

	// --- Provider Presets ---
	mux.HandleFunc("/api/provider-presets", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		type presetResponse struct {
			provider.Preset
			Available     bool     `json:"available"`
			FetchedModels []string `json:"fetchedModels,omitempty"`
		}

		results := make([]presetResponse, 0, len(provider.Presets))
		for _, p := range provider.Presets {
			pr := presetResponse{Preset: p, Available: true}
			if p.Dynamic {
				models, err := provider.FetchPresetModels(p)
				if err != nil {
					pr.Available = false
				} else {
					pr.FetchedModels = models
				}
			}
			results = append(results, pr)
		}
		json.NewEncoder(w).Encode(results)
	})

	// --- Provider Test ---
	mux.HandleFunc("/api/provider-test", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		var req struct {
			Type    string `json:"type"`
			BaseURL string `json:"baseUrl"`
			APIKey  string `json:"apiKey"`
			Model   string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Model == "" {
			http.Error(w, `{"error":"model is required"}`, http.StatusBadRequest)
			return
		}

		// Resolve preset. For known presets, always use the registry BaseURL
		// (ignoring user-supplied baseUrl) to prevent SSRF. For unknown/custom
		// types, validate the user-supplied URL before using it.
		var preset provider.Preset
		if p, ok := provider.GetPreset(req.Type); ok {
			preset = p
		} else {
			if req.BaseURL == "" {
				http.Error(w, `{"error":"baseUrl is required for custom provider"}`, http.StatusBadRequest)
				return
			}
			if err := provider.ValidateCustomURL(req.BaseURL); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
				return
			}
			preset = provider.Preset{
				Name:    "custom",
				BaseURL: strings.TrimRight(req.BaseURL, "/"),
			}
		}

		start := time.Now()
		err := provider.TestPresetConnection(preset, req.APIKey, req.Model)
		latency := time.Since(start).Milliseconds()
		if err != nil {
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error(), "latencyMs": latency})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "latencyMs": latency})
	})

	// --- Provider Config Save ---
	mux.HandleFunc("/api/config/providers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			cfg2 := s.Cfg()
			type providerEntry struct {
				Name   string                     `json:"name"`
				Config tetoraConfig.ProviderConfig `json:"config"`
			}
			var entries []providerEntry
			for name, pc := range cfg2.Providers {
				// Mask API key for UI
				masked := pc
				if masked.APIKey != "" {
					masked.APIKey = "***"
				}
				entries = append(entries, providerEntry{Name: name, Config: masked})
			}
			// Sort by name for stable output
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
			json.NewEncoder(w).Encode(entries)

		case http.MethodPut:
			var req struct {
				Name   string                     `json:"name"`
				Config tetoraConfig.ProviderConfig `json:"config"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				jsonError(w, err.Error(), http.StatusBadRequest)
				return
			}
			if req.Name == "" {
				http.Error(w, `{"error":"name is required"}`, http.StatusBadRequest)
				return
			}
			if req.Config.Type == "" {
				http.Error(w, `{"error":"config.type is required"}`, http.StatusBadRequest)
				return
			}
			configPath := findConfigPath()
			if err := tetoraConfig.SaveProviders(configPath, req.Name, req.Config); err != nil {
				jsonError(w, err.Error(), http.StatusInternalServerError)
				return
			}
			signalSelfReload()
			audit.Log(cfg.HistoryDB, "config.provider.save", "dashboard",
				fmt.Sprintf("provider=%s type=%s", req.Name, req.Config.Type), "")
			json.NewEncoder(w).Encode(map[string]string{"status": "ok", "name": req.Name})

		case http.MethodDelete:
			name := r.URL.Query().Get("name")
			if name == "" {
				http.Error(w, `{"error":"name query param required"}`, http.StatusBadRequest)
				return
			}
			configPath := findConfigPath()
			if err := tetoraConfig.DeleteProvider(configPath, name); err != nil {
				jsonError(w, err.Error(), http.StatusInternalServerError)
				return
			}
			signalSelfReload()
			audit.Log(cfg.HistoryDB, "config.provider.delete", "dashboard", fmt.Sprintf("provider=%s", name), "")
			json.NewEncoder(w).Encode(map[string]string{"status": "ok", "name": name})

		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	})

	// --- Inference Mode (cloud/local toggle) ---
	mux.HandleFunc("/api/inference-mode", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			cfg2 := s.Cfg()
			mode := cfg2.InferenceMode
			if mode == "" {
				mode = "mixed"
			}
			// Count agents by mode.
			cloud, local := 0, 0
			for _, ac := range cfg2.Agents {
				p := ac.Provider
				if p == "" {
					p = cfg2.DefaultProvider
				}
				if provider.IsLocalProvider(p) {
					local++
				} else {
					cloud++
				}
			}
			json.NewEncoder(w).Encode(map[string]any{
				"mode": mode, "cloud": cloud, "local": local, "total": cloud + local,
			})

		case http.MethodPost:
			var req struct {
				Mode string `json:"mode"` // "cloud" | "local" | "mixed"
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				jsonError(w, err.Error(), http.StatusBadRequest)
				return
			}
			if req.Mode != "cloud" && req.Mode != "local" && req.Mode != "mixed" {
				jsonError(w, `mode must be "cloud", "local", or "mixed"`, http.StatusBadRequest)
				return
			}

			cfg2 := s.Cfg()

			// For "local" mode, verify Ollama is reachable and get available models.
			var ollamaModels []string
			if req.Mode == "local" {
				ollamaPreset, _ := provider.GetPreset("ollama")
				models, err := provider.FetchPresetModels(ollamaPreset)
				if err != nil || len(models) == 0 {
					jsonError(w, "Ollama is not reachable or has no models. Start it with: ollama serve", http.StatusServiceUnavailable)
					return
				}
				ollamaModels = models
			}

			switched := 0
			pinned := 0
			var errors []string
			updatedAgents := make(map[string]tetoraConfig.AgentConfig)

			for name, ac := range cfg2.Agents {
				// Skip agents with PinMode set.
				if ac.PinMode != "" {
					pinned++
					continue
				}

				switch req.Mode {
				case "local":
					// Already on a local provider? Skip.
					if provider.IsLocalProvider(ac.Provider) {
						continue
					}
					// Save current cloud model.
					ac.CloudModel = ac.Model
					// Use agent's preferred local model, or first Ollama model.
					if ac.LocalModel != "" {
						ac.Model = ac.LocalModel
					} else if len(ollamaModels) > 0 {
						ac.Model = ollamaModels[0]
					} else {
						errors = append(errors, name+": no local model available")
						continue
					}
					ac.Provider = "ollama"
					switched++

				case "cloud":
					// Not on a local provider? Skip.
					if !provider.IsLocalProvider(ac.Provider) {
						continue
					}
					// Restore cloud model.
					if ac.CloudModel != "" {
						ac.Model = ac.CloudModel
						// Infer provider from the cloud model name.
						if preset, ok := provider.InferProviderFromModelWithPref(ac.CloudModel, cfg2.ClaudeProvider); ok {
							ac.Provider = preset
						} else {
							ac.Provider = cfg2.DefaultProvider
						}
					} else {
						// No saved cloud model — use defaults.
						ac.Model = cfg2.DefaultModel
						ac.Provider = cfg2.DefaultProvider
					}
					switched++

				case "mixed":
					// Just set the mode flag, don't change any models.
				}

				cfg2.Agents[name] = ac
				updatedAgents[name] = ac
			}

			// Persist to config.json in a single atomic write.
			configPath := findConfigPath()
			modeStr := req.Mode
			if modeStr == "mixed" {
				modeStr = "" // empty = mixed in config
			}
			if err := tetoraConfig.SaveInferenceMode(configPath, modeStr, updatedAgents); err != nil {
				jsonError(w, "save config: "+err.Error(), http.StatusInternalServerError)
				return
			}
			cfg2.InferenceMode = modeStr

			signalSelfReload()
			audit.Log(cfg.HistoryDB, "config.inference_mode", "dashboard",
				fmt.Sprintf("mode=%s switched=%d pinned=%d", req.Mode, switched, pinned), "")

			json.NewEncoder(w).Encode(map[string]any{
				"mode":    req.Mode,
				"switched": switched,
				"pinned":  pinned,
				"errors":  errors,
			})

		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	})

	// --- P18.2: OAuth 2.0 Framework ---
	oauthMgr := newOAuthManager(cfg)
	globalOAuthManager = oauthMgr // expose for Gmail/Calendar tools
	mux.HandleFunc("/api/oauth/services", oauthMgr.HandleOAuthServices)
	mux.HandleFunc("/api/oauth/", oauthMgr.HandleOAuthRoute)

	// --- Drain: graceful shutdown (stop accepting new tasks, wait for running to finish) ---
	mux.HandleFunc("/api/admin/drain", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		state.mu.Lock()
		alreadyDraining := state.draining
		state.draining = true
		active := len(state.running)
		state.mu.Unlock()

		audit.Log(cfg.HistoryDB, "admin.drain", "http", fmt.Sprintf("active=%d", active), clientIP(r))
		log.Info("drain requested via API", "activeAgents", active)

		// Signal the main loop to begin draining (if channel is wired up).
		if s.drainCh != nil && !alreadyDraining {
			select {
			case s.drainCh <- struct{}{}:
			default:
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status":   "draining",
			"active":   active,
			"message":  "daemon will stop accepting new tasks and exit after current tasks complete",
		})
	})

	// --- Workspace File Browser ---
	mux.HandleFunc("GET /api/workspace/files", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		wsDir := cfg.WorkspaceDir
		if wsDir == "" {
			json.NewEncoder(w).Encode(map[string]any{"entries": []any{}})
			return
		}

		// Determine target directory from ?dir= parameter
		dirParam := r.URL.Query().Get("dir")
		targetDir := wsDir
		if dirParam != "" {
			clean := filepath.Clean(dirParam)
			if strings.Contains(clean, "..") {
				http.Error(w, `{"error":"invalid path"}`, http.StatusBadRequest)
				return
			}
			targetDir = filepath.Join(wsDir, clean)
			if !strings.HasPrefix(targetDir, wsDir) {
				http.Error(w, `{"error":"invalid path"}`, http.StatusBadRequest)
				return
			}
		}

		type wsEntry struct {
			Path    string `json:"path"`
			Name    string `json:"name"`
			IsDir   bool   `json:"isDir"`
			Size    int64  `json:"size"`
			ModTime string `json:"modTime"`
		}

		// Hidden/build directories to skip
		skipDirs := map[string]bool{
			".git": true, "node_modules": true, "__pycache__": true, ".next": true,
			".venv": true, ".mypy_cache": true, ".pytest_cache": true,
		}

		// Allowed file extensions (whitelist)
		allowedExts := map[string]bool{
			".md": true, ".txt": true, ".json": true, ".yaml": true, ".yml": true,
			".toml": true, ".sh": true, ".go": true, ".py": true, ".js": true,
			".ts": true, ".css": true, ".html": true, ".csv": true, ".xml": true,
			".cfg": true, ".ini": true, ".env": true, ".sql": true,
		}

		dirEntries, err := os.ReadDir(targetDir)
		if err != nil {
			json.NewEncoder(w).Encode(map[string]any{"entries": []any{}})
			return
		}

		entries := make([]wsEntry, 0)
		for _, e := range dirEntries {
			name := e.Name()
			entryPath := filepath.Join(targetDir, name)

			// Follow symlinks once: use os.Stat for both isDir and metadata.
			// For non-symlinks, fall back to DirEntry info (no extra syscall).
			isDir := e.IsDir()
			var size int64
			var modTime string
			if e.Type()&fs.ModeSymlink != 0 {
				if info, err := os.Stat(entryPath); err == nil {
					isDir = info.IsDir()
					size = info.Size()
					modTime = info.ModTime().Format(time.RFC3339)
				}
				// dangling symlink: isDir stays false, entry is silently skipped by ext check
			} else {
				if info, err := e.Info(); err == nil {
					size = info.Size()
					modTime = info.ModTime().Format(time.RFC3339)
				}
			}

			if isDir {
				if skipDirs[name] || strings.HasPrefix(name, ".") {
					continue
				}
			} else {
				ext := strings.ToLower(filepath.Ext(name))
				if !allowedExts[ext] {
					continue
				}
			}

			// Build path relative to workspace root
			var relPath string
			if dirParam != "" {
				relPath = filepath.Clean(dirParam) + "/" + name
			} else {
				relPath = name
			}

			entries = append(entries, wsEntry{
				Path:    relPath,
				Name:    name,
				IsDir:   isDir,
				Size:    size,
				ModTime: modTime,
			})
		}
		json.NewEncoder(w).Encode(map[string]any{"entries": entries})
	})

	mux.HandleFunc("GET /api/workspace/file", func(w http.ResponseWriter, r *http.Request) {
		wsDir := cfg.WorkspaceDir
		p := r.URL.Query().Get("path")
		if wsDir == "" || p == "" {
			http.Error(w, `{"error":"missing path"}`, http.StatusBadRequest)
			return
		}
		// Security: prevent path traversal
		clean := filepath.Clean(p)
		if strings.Contains(clean, "..") {
			http.Error(w, `{"error":"invalid path"}`, http.StatusBadRequest)
			return
		}
		full := filepath.Join(wsDir, clean)
		if !strings.HasPrefix(full, wsDir) {
			http.Error(w, `{"error":"invalid path"}`, http.StatusBadRequest)
			return
		}
		data, err := os.ReadFile(full)
		if err != nil {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"path": p, "content": string(data)})
	})

	mux.HandleFunc("PUT /api/workspace/file", func(w http.ResponseWriter, r *http.Request) {
		wsDir := cfg.WorkspaceDir
		if wsDir == "" {
			http.Error(w, `{"error":"no workspace"}`, http.StatusBadRequest)
			return
		}
		var req struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
			return
		}
		clean := filepath.Clean(req.Path)
		if strings.Contains(clean, "..") {
			http.Error(w, `{"error":"invalid path"}`, http.StatusBadRequest)
			return
		}
		full := filepath.Join(wsDir, clean)
		if !strings.HasPrefix(full, wsDir) {
			http.Error(w, `{"error":"invalid path"}`, http.StatusBadRequest)
			return
		}
		if err := os.WriteFile(full, []byte(req.Content), 0644); err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
}

// ---------------------------------------------------------------------------
// plan_review.go — Plan Review System.
// Tracks plan mode approvals for rich Discord/Dashboard review experience.
// ---------------------------------------------------------------------------

// PlanReview represents a plan waiting for or having received review.
type PlanReview struct {
	ID         string `json:"id"`
	SessionID  string `json:"sessionId"`
	WorkerName string `json:"workerName,omitempty"`
	Agent      string `json:"agent,omitempty"`
	PlanText   string `json:"planText"`
	Status     string `json:"status"` // pending, approved, rejected
	Reviewer   string `json:"reviewer,omitempty"`
	ReviewNote string `json:"reviewNote,omitempty"`
	CreatedAt  string `json:"createdAt"`
	ReviewedAt string `json:"reviewedAt,omitempty"`
}

// initPlanReviewDB creates the plan_reviews table if it doesn't exist.
func initPlanReviewDB(dbPath string) error {
	sql := `
CREATE TABLE IF NOT EXISTS plan_reviews (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    worker_name TEXT DEFAULT '',
    agent TEXT DEFAULT '',
    plan_text TEXT NOT NULL,
    status TEXT DEFAULT 'pending',
    reviewer TEXT DEFAULT '',
    review_note TEXT DEFAULT '',
    created_at TEXT NOT NULL,
    reviewed_at TEXT DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_plan_reviews_session ON plan_reviews(session_id);
CREATE INDEX IF NOT EXISTS idx_plan_reviews_status ON plan_reviews(status);
`
	return db.Exec(dbPath, sql)
}

// insertPlanReview creates a new plan review record.
func insertPlanReview(dbPath string, review *PlanReview) error {
	sql := fmt.Sprintf(
		`INSERT OR REPLACE INTO plan_reviews (id, session_id, worker_name, agent, plan_text, status, created_at)
		 VALUES ('%s','%s','%s','%s','%s','pending','%s')`,
		db.Escape(review.ID),
		db.Escape(review.SessionID),
		db.Escape(review.WorkerName),
		db.Escape(review.Agent),
		db.Escape(review.PlanText),
		db.Escape(review.CreatedAt),
	)
	return db.Exec(dbPath, sql)
}

// updatePlanReviewStatus marks a plan review as approved or rejected.
func updatePlanReviewStatus(dbPath, id, status, reviewer, note string) error {
	sql := fmt.Sprintf(
		`UPDATE plan_reviews SET status='%s', reviewer='%s', review_note='%s', reviewed_at='%s' WHERE id='%s'`,
		db.Escape(status),
		db.Escape(reviewer),
		db.Escape(note),
		db.Escape(time.Now().Format(time.RFC3339)),
		db.Escape(id),
	)
	return db.Exec(dbPath, sql)
}

// listPendingPlanReviews returns all pending plan reviews.
func listPendingPlanReviews(dbPath string) ([]PlanReview, error) {
	sql := `SELECT id, session_id, worker_name, agent, plan_text, status, reviewer, review_note, created_at, reviewed_at FROM plan_reviews WHERE status='pending' ORDER BY created_at DESC`
	return queryPlanReviews(dbPath, sql)
}

// listRecentPlanReviews returns recent plan reviews (all statuses).
func listRecentPlanReviews(dbPath string, limit int) ([]PlanReview, error) {
	if limit <= 0 {
		limit = 20
	}
	sql := fmt.Sprintf(`SELECT id, session_id, worker_name, agent, plan_text, status, reviewer, review_note, created_at, reviewed_at FROM plan_reviews ORDER BY created_at DESC LIMIT %d`, limit)
	return queryPlanReviews(dbPath, sql)
}

func queryPlanReviews(dbPath, sqlStr string) ([]PlanReview, error) {
	rows, err := db.Query(dbPath, sqlStr)
	if err != nil {
		return nil, err
	}
	var reviews []PlanReview
	for _, row := range rows {
		reviews = append(reviews, PlanReview{
			ID:         mapStr(row, "id"),
			SessionID:  mapStr(row, "session_id"),
			WorkerName: mapStr(row, "worker_name"),
			Agent:      mapStr(row, "agent"),
			PlanText:   mapStr(row, "plan_text"),
			Status:     mapStr(row, "status"),
			Reviewer:   mapStr(row, "reviewer"),
			ReviewNote: mapStr(row, "review_note"),
			CreatedAt:  mapStr(row, "created_at"),
			ReviewedAt: mapStr(row, "reviewed_at"),
		})
	}
	return reviews, nil
}

func mapStr(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// --- HTTP API ---

// registerPlanReviewRoutes registers plan review API endpoints.
func (s *Server) registerPlanReviewRoutes(mux *http.ServeMux) {
	cfg := s.cfg

	// GET /api/plan-reviews — list plan reviews.
	mux.HandleFunc("/api/plan-reviews", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}

		status := r.URL.Query().Get("status")
		var reviews []PlanReview
		var err error
		if status == "pending" {
			reviews, err = listPendingPlanReviews(cfg.HistoryDB)
		} else {
			reviews, err = listRecentPlanReviews(cfg.HistoryDB, 50)
		}
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if reviews == nil {
			reviews = []PlanReview{}
		}
		json.NewEncoder(w).Encode(reviews)
	})

	// POST /api/plan-reviews/{id}/approve — approve a plan.
	// POST /api/plan-reviews/{id}/reject — reject a plan.
	mux.HandleFunc("/api/plan-reviews/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}

		path := strings.TrimPrefix(r.URL.Path, "/api/plan-reviews/")
		parts := strings.Split(path, "/")
		if len(parts) != 2 || parts[0] == "" {
			http.Error(w, `{"error":"invalid path, use /api/plan-reviews/{id}/approve or /reject"}`, http.StatusBadRequest)
			return
		}

		reviewID := parts[0]
		action := parts[1]

		if action != "approve" && action != "reject" {
			http.Error(w, `{"error":"action must be approve or reject"}`, http.StatusBadRequest)
			return
		}

		var body struct {
			Reviewer string `json:"reviewer"`
			Note     string `json:"note"`
		}
		json.NewDecoder(r.Body).Decode(&body)

		status := "approved"
		if action == "reject" {
			status = "rejected"
		}

		if err := updatePlanReviewStatus(cfg.HistoryDB, reviewID, status, body.Reviewer, body.Note); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Publish review decision to SSE.
		if s.hookReceiver != nil && s.hookReceiver.Broker() != nil {
			s.hookReceiver.Broker().Publish(SSEDashboardKey, SSEEvent{
				Type: SSEPlanReview,
				Data: map[string]any{
					"reviewId": reviewID,
					"action":   action,
					"reviewer": body.Reviewer,
				},
			})
		}

		audit.Log(cfg.HistoryDB, "plan_review."+action, "http",
			fmt.Sprintf("id=%s reviewer=%s", reviewID, body.Reviewer), clientIP(r))

		json.NewEncoder(w).Encode(map[string]string{"status": "ok", "action": action})
	})
}

// --- Discord Plan Review Formatting ---

// buildPlanReviewEmbed creates a rich Discord embed for plan review.
func buildPlanReviewEmbed(review *PlanReview) discord.Embed {
	color := 0x3498db // Blue for pending

	// Truncate plan text for Discord embed (max 4096 chars for description).
	planPreview := review.PlanText
	if len(planPreview) > 3500 {
		planPreview = planPreview[:3500] + "\n\n... (truncated, see dashboard for full plan)"
	}

	embed := discord.Embed{
		Title:       "Plan Review Required",
		Description: planPreview,
		Color:       color,
		Fields: []discord.EmbedField{
			{Name: "Session", Value: truncate(review.SessionID, 36), Inline: true},
		},
		Timestamp: review.CreatedAt,
	}

	if review.Agent != "" {
		embed.Fields = append(embed.Fields, discord.EmbedField{
			Name: "Agent", Value: review.Agent, Inline: true,
		})
	}
	if review.WorkerName != "" {
		embed.Fields = append(embed.Fields, discord.EmbedField{
			Name: "Worker", Value: review.WorkerName, Inline: true,
		})
	}

	return embed
}

// buildPlanReviewComponents creates Approve/Reject/Request Changes buttons.
func buildPlanReviewComponents(reviewID string) []discord.Component {
	return []discord.Component{
		discordActionRow(
			discordButton("plan_approve:"+reviewID, "Approve", discord.ButtonStyleSuccess),
			discordButton("plan_reject:"+reviewID, "Reject", discord.ButtonStyleDanger),
		),
	}
}

func (s *Server) registerDispatchRoutes(mux *http.ServeMux) {
	state := s.state
	sem := s.sem
	childSem := s.childSem
	cron := s.cron

	// --- Dashboard SSE Stream ---
	mux.HandleFunc("/events/dashboard", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		if state.broker == nil {
			http.Error(w, `{"error":"streaming not available"}`, http.StatusServiceUnavailable)
			return
		}
		serveDashboardSSE(w, r, state.broker)
	})

	// --- Sprite Config + Assets ---
	spritesDir := filepath.Join(s.cfg.BaseDir, "media", "sprites")
	mux.HandleFunc("/api/sprites/config", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		cfg := s.Cfg()
		var agentKeys []string
		for k := range cfg.Agents {
			agentKeys = append(agentKeys, k)
		}
		spriteCfg := loadSpriteConfig(spritesDir, agentKeys)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(spriteCfg)
	})
	mux.HandleFunc("/media/sprites/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		name := filepath.Base(r.URL.Path)
		if name == "." || name == "/" || strings.Contains(name, "..") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=86400")
		http.ServeFile(w, r, filepath.Join(spritesDir, name))
	})

	// Built-in portraits (go:embed)
	mux.HandleFunc("/dashboard/portraits/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		name := filepath.Base(r.URL.Path)
		if name == "." || name == "/" || strings.Contains(name, "..") {
			http.NotFound(w, r)
			return
		}
		f, err := portraitFS.Open("assets/portraits/" + name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		http.ServeContent(w, r, name, time.Time{}, f.(io.ReadSeeker))
	})

	// User-uploaded custom portraits
	portraitsDir := filepath.Join(s.cfg.BaseDir, "media", "portraits")
	mux.HandleFunc("/media/portraits/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			name := filepath.Base(r.URL.Path)
			if name == "." || name == "/" || strings.Contains(name, "..") {
				http.NotFound(w, r)
				return
			}
			info, err := os.Stat(filepath.Join(portraitsDir, name))
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Cache-Control", "public, max-age=300")
			w.Header().Set("Content-Type", "image/png")
			http.ServeFile(w, r, filepath.Join(portraitsDir, name))
			_ = info
		default:
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
		}
	})

	// Portrait upload: POST /api/agents/{name}/portrait
	//                  DELETE /api/agents/{name}/portrait
	mux.HandleFunc("/api/agents/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/agents/")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) != 2 || parts[1] != "portrait" {
			http.NotFound(w, r)
			return
		}
		agentName := parts[0]
		if agentName == "" || strings.Contains(agentName, "..") {
			http.Error(w, `{"error":"invalid agent name"}`, http.StatusBadRequest)
			return
		}

		switch r.Method {
		case http.MethodPost:
			if err := r.ParseMultipartForm(512 * 1024); err != nil {
				http.Error(w, `{"error":"file too large (max 512KB)"}`, http.StatusBadRequest)
				return
			}
			file, _, err := r.FormFile("portrait")
			if err != nil {
				http.Error(w, `{"error":"portrait field missing"}`, http.StatusBadRequest)
				return
			}
			defer file.Close()
			// Validate PNG magic bytes
			magic := make([]byte, 8)
			if _, err := file.Read(magic); err != nil || string(magic) != "\x89PNG\r\n\x1a\n" {
				http.Error(w, `{"error":"file must be a PNG"}`, http.StatusBadRequest)
				return
			}
			if err := os.MkdirAll(portraitsDir, 0755); err != nil {
				http.Error(w, `{"error":"storage error"}`, http.StatusInternalServerError)
				return
			}
			dst := filepath.Join(portraitsDir, agentName+".png")
			f, err := os.Create(dst)
			if err != nil {
				http.Error(w, `{"error":"storage error"}`, http.StatusInternalServerError)
				return
			}
			defer f.Close()
			f.Write(magic)
			io.Copy(f, file)
			info, _ := os.Stat(dst)
			mtime := ""
			if info != nil {
				mtime = fmt.Sprintf("%d", info.ModTime().Unix())
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"portraitURL":"/media/portraits/%s.png?v=%s"}`, agentName, mtime)

		case http.MethodDelete:
			dst := filepath.Join(portraitsDir, agentName+".png")
			if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
				http.Error(w, `{"error":"delete failed"}`, http.StatusInternalServerError)
				return
			}
			newURL := resolvePortraitURL(s.cfg.BaseDir, agentName)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"portraitURL":"%s"}`, newURL)

		default:
			http.Error(w, `{"error":"POST or DELETE only"}`, http.StatusMethodNotAllowed)
		}
	})

	// --- Offline Queue ---
	mux.HandleFunc("/queue", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		cfg := s.Cfg()
		w.Header().Set("Content-Type", "application/json")
		status := r.URL.Query().Get("status")
		items := queryQueue(cfg.HistoryDB, status)
		if items == nil {
			items = []QueueItem{}
		}
		json.NewEncoder(w).Encode(map[string]any{
			"items":   items,
			"count":   len(items),
			"pending": countPendingQueue(cfg.HistoryDB),
		})
	})

	mux.HandleFunc("/queue/", func(w http.ResponseWriter, r *http.Request) {
		cfg := s.Cfg()
		w.Header().Set("Content-Type", "application/json")
		path := strings.TrimPrefix(r.URL.Path, "/queue/")

		// POST /queue/{id}/retry
		if strings.HasSuffix(path, "/retry") {
			if r.Method != http.MethodPost {
				http.Error(w, "POST only", http.StatusMethodNotAllowed)
				return
			}
			idStr := strings.TrimSuffix(path, "/retry")
			id, err := strconv.Atoi(idStr)
			if err != nil {
				http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
				return
			}
			item := queryQueueItem(cfg.HistoryDB, id)
			if item == nil {
				http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
				return
			}
			if item.Status != "pending" && item.Status != "failed" {
				jsonError(w, fmt.Sprintf("item status is %q, must be pending or failed", item.Status), http.StatusConflict)
				return
			}

			// Deserialize and re-dispatch.
			var task Task
			if err := json.Unmarshal([]byte(item.TaskJSON), &task); err != nil {
				http.Error(w, `{"error":"invalid task in queue"}`, http.StatusInternalServerError)
				return
			}
			task.ID = newUUID()
			task.SessionID = newUUID()
			task.Source = "queue-retry:" + task.Source

			updateQueueStatus(cfg.HistoryDB, id, "processing", "")
			audit.Log(cfg.HistoryDB, "queue.retry", "http", fmt.Sprintf("queueId=%d", id), clientIP(r))

			go func() {
				ctx := trace.WithID(context.Background(), trace.NewID("queue"))
				result := runSingleTask(ctx, cfg, task, sem, childSem, item.AgentName)
				if result.Status == "success" {
					updateQueueStatus(cfg.HistoryDB, id, "completed", "")
				} else {
					incrementQueueRetry(cfg.HistoryDB, id, "failed", result.Error)
				}
				startAt := time.Now().Add(-time.Duration(result.DurationMs) * time.Millisecond)
				recordHistory(cfg.HistoryDB, task.ID, task.Name, task.Source, item.AgentName, task, result,
					startAt.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)
			}()

			w.Write([]byte(fmt.Sprintf(`{"status":"retrying","taskId":%q}`, task.ID)))
			return
		}

		// GET /queue/{id} or DELETE /queue/{id}
		id, err := strconv.Atoi(path)
		if err != nil {
			http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
			return
		}

		switch r.Method {
		case http.MethodGet:
			item := queryQueueItem(cfg.HistoryDB, id)
			if item == nil {
				http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(item)

		case http.MethodDelete:
			item := queryQueueItem(cfg.HistoryDB, id)
			if item == nil {
				http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
				return
			}
			if err := deleteQueueItem(cfg.HistoryDB, id); err != nil {
				jsonError(w, err.Error(), http.StatusInternalServerError)
				return
			}
			audit.Log(cfg.HistoryDB, "queue.delete", "http", fmt.Sprintf("queueId=%d", id), clientIP(r))
			w.Write([]byte(`{"status":"deleted"}`))

		default:
			http.Error(w, "GET or DELETE only", http.StatusMethodNotAllowed)
		}
	})

	// --- Dispatch ---
	mux.HandleFunc("/dispatch", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		cfg := s.Cfg()

		// Resolve per-client dispatch state and semaphores.
		clientID := getClientID(r)
		cState, cSem, cChildSem := s.resolveClientDispatch(clientID)

		// Allow sub-agent dispatches to run concurrently with parent tasks.
		// Only block duplicate batch dispatches from external callers.
		// Note: X-Tetora-Source is an operational signal, not a security boundary.
		// Any authenticated client can set it; the guard only prevents accidental
		// double-dispatch, not unauthorized access.
		isSubAgent := r.Header.Get("X-Tetora-Source") == "agent_dispatch"
		if !isSubAgent {
			cState.mu.Lock()
			busy := cState.active
			cState.mu.Unlock()
			if busy {
				http.Error(w, `{"error":"dispatch already running"}`, http.StatusConflict)
				return
			}
		}

		var tasks []Task
		if err := json.NewDecoder(r.Body).Decode(&tasks); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		if len(tasks) == 0 {
			http.Error(w, `{"error":"empty task list"}`, http.StatusBadRequest)
			return
		}

		// --- Dispatch guardrails: validate payload before execution ---
		for i, t := range tasks {
			// Prompt is required — empty prompt wastes agent time.
			if strings.TrimSpace(t.Prompt) == "" && strings.TrimSpace(t.Name) == "" {
				jsonError(w, fmt.Sprintf("task[%d]: prompt is required", i), http.StatusBadRequest)
				return
			}
			// If agent is specified, verify it exists in config.
			if t.Agent != "" {
				if _, ok := cfg.Agents[t.Agent]; !ok {
					jsonError(w, fmt.Sprintf("task[%d]: agent %q not found in config", i, t.Agent), http.StatusBadRequest)
					return
				}
			}
		}

		for i := range tasks {
			fillDefaults(cfg, &tasks[i])
			tasks[i].Source = "http"
			tasks[i].ClientID = clientID
		}

		// Log dispatch payload for audit trail.
		for _, t := range tasks {
			log.Info("dispatch: received task", "name", t.Name, "agent", t.Agent,
				"source", "http", "prompt_len", len(t.Prompt), "model", t.Model)
		}

		// Reset stale "doing" tasks before dispatch to prevent blocking.
		if s.taskBoardDispatcher != nil {
			s.taskBoardDispatcher.ResetStuckDoing()
		}

		// Per-agent concurrency guard: reject if any target agent already has
		// too many tasks in 'doing' state. Configurable via maxTasksPerAgent
		// (default 1).
		if s.taskBoardDispatcher != nil {
			maxPerAgent := cfg.TaskBoard.AutoDispatch.MaxTasksPerAgentOrDefault()
			for i, t := range tasks {
				if t.Agent == "" {
					continue
				}
				if n := s.taskBoardDispatcher.DoingTaskCountForAgent(t.Agent); n >= maxPerAgent {
					log.Warn("dispatch: agent at per-agent concurrency limit",
						"agent", t.Agent, "taskName", t.Name, "doing", n, "max", maxPerAgent)
					jsonError(w, fmt.Sprintf(
						"task[%d]: agent %q at concurrency limit (%d/%d active tasks)",
						i, t.Agent, n, maxPerAgent), http.StatusConflict)
					return
				}
			}
		}

		// Resolve per-client audit DB path.
		auditDB := s.resolveHistoryDB(cfg, clientID)

		// Publish task_received to dashboard.
		if cState.broker != nil {
			for _, t := range tasks {
				cState.broker.Publish(SSEDashboardKey, SSEEvent{
					Type: SSETaskReceived,
					Data: map[string]any{
						"source": "http",
						"prompt": truncate(t.Prompt, 200),
					},
				})
			}
		}

		audit.Log(auditDB, "dispatch", "http",
			fmt.Sprintf("%d tasks (client=%s)", len(tasks), clientID), clientIP(r))

		// Decouple from HTTP request lifecycle so client disconnect doesn't kill in-flight tasks.
		dispatchCtx := trace.WithID(context.Background(), trace.IDFromContext(r.Context()))
		result := dispatch(dispatchCtx, cfg, tasks, cState, cSem, cChildSem, isSubAgent)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	})

	// --- Cancel ---
	mux.HandleFunc("/cancel", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		clientID := getClientID(r)
		cState, _, _ := s.resolveClientDispatch(clientID)
		cState.mu.Lock()
		cancelFn := cState.cancel
		cState.mu.Unlock()
		if cancelFn == nil {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"nothing to cancel"}`))
			return
		}
		cancelFn()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"cancelling"}`))
	})

	// --- Cancel single task ---
	mux.HandleFunc("/cancel/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		cfg := s.Cfg()
		clientID := getClientID(r)
		cState, _, _ := s.resolveClientDispatch(clientID)
		auditDB := s.resolveHistoryDB(cfg, clientID)
		w.Header().Set("Content-Type", "application/json")

		id := strings.TrimPrefix(r.URL.Path, "/cancel/")
		if id == "" {
			http.Error(w, `{"error":"id required"}`, http.StatusBadRequest)
			return
		}

		// Try dispatch state first.
		cState.mu.Lock()
		if ts, ok := cState.running[id]; ok && ts.cancelFn != nil {
			ts.cancelFn()
			cState.mu.Unlock()
			audit.Log(auditDB, "task.cancel", "http",
				fmt.Sprintf("id=%s (dispatch)", id), clientIP(r))
			w.Write([]byte(`{"status":"cancelling"}`))
			return
		}
		cState.mu.Unlock()

		// Try cron engine.
		if cron != nil {
			if err := cron.CancelJob(id); err == nil {
				audit.Log(auditDB, "job.cancel", "http",
					fmt.Sprintf("id=%s (cron)", id), clientIP(r))
				w.Write([]byte(`{"status":"cancelling"}`))
				return
			}
		}

		http.Error(w, `{"error":"task not found or not running"}`, http.StatusNotFound)
	})

	// --- Running Tasks ---
	mux.HandleFunc("/tasks/running", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		type runningTask struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Source   string `json:"source"`
			Model    string `json:"model"`
			Timeout  string `json:"timeout"`
			Elapsed  string `json:"elapsed"`
			Prompt   string `json:"prompt,omitempty"`
			PID      int    `json:"pid,omitempty"`
			PIDAlive bool   `json:"pidAlive"`
			Agent     string `json:"agent,omitempty"`
			ParentID string `json:"parentId,omitempty"`
			Depth    int    `json:"depth,omitempty"`
		}

		var tasks []runningTask

		// From dispatch state (per-client).
		clientID := getClientID(r)
		cState, _, _ := s.resolveClientDispatch(clientID)
		cState.mu.Lock()
		for _, ts := range cState.running {
			prompt := ts.task.Prompt
			if len(prompt) > 100 {
				prompt = prompt[:100] + "..."
			}
			pid := 0
			pidAlive := false
			if ts.cmd != nil && ts.cmd.Process != nil {
				pid = ts.cmd.Process.Pid
				// On Unix, sending signal 0 checks if process exists.
				if ts.cmd.Process.Signal(syscall.Signal(0)) == nil {
					pidAlive = true
				}
			}
			tasks = append(tasks, runningTask{
				ID:       ts.task.ID,
				Name:     ts.task.Name,
				Source:   ts.task.Source,
				Model:    ts.task.Model,
				Timeout:  ts.task.Timeout,
				Elapsed:  time.Since(ts.startAt).Round(time.Second).String(),
				Prompt:   prompt,
				PID:      pid,
				PIDAlive: pidAlive,
				Agent:     ts.task.Agent,
				ParentID: ts.task.ParentID,
				Depth:    ts.task.Depth,
			})
		}
		cState.mu.Unlock()

		// From cron engine.
		if cron != nil {
			for _, j := range cron.ListJobs() {
				if !j.Running {
					continue
				}
				tasks = append(tasks, runningTask{
					ID:      j.ID,
					Name:    j.Name,
					Source:  "cron",
					Model:   j.RunModel,
					Timeout: j.RunTimeout,
					Elapsed: j.RunElapsed,
					Prompt:  j.RunPrompt,
				})
			}
		}

		if tasks == nil {
			tasks = []runningTask{}
		}
		json.NewEncoder(w).Encode(tasks)
	})

	// --- Tasks (History DB) ---
	mux.HandleFunc("/tasks", func(w http.ResponseWriter, r *http.Request) {
		cfg := s.Cfg()
		dbPath := s.resolveHistoryDB(cfg, getClientID(r))
		if dbPath == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}

		switch r.Method {
		case http.MethodGet:
			status := r.URL.Query().Get("status")
			if status != "" {
				tasks, err := db.GetTasksByStatus(dbPath, status)
				if err != nil {
					jsonError(w, err.Error(), http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(tasks)
			} else {
				stats, err := db.GetTaskStats(dbPath)
				if err != nil {
					jsonError(w, err.Error(), http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(stats)
			}

		case http.MethodPatch:
			var body struct {
				ID     string `json:"id"`
				Status string `json:"status"`
				Error  string `json:"error"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				jsonError(w, err.Error(), http.StatusBadRequest)
				return
			}
			if err := db.UpdateTaskStatus(dbPath, body.ID, body.Status, body.Error); err != nil {
				jsonError(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok"}`))

		default:
			http.Error(w, "GET or PATCH only", http.StatusMethodNotAllowed)
		}
	})

	// --- Output files ---
	mux.HandleFunc("/outputs/", func(w http.ResponseWriter, r *http.Request) {
		cfg := s.Cfg()
		name := strings.TrimPrefix(r.URL.Path, "/outputs/")
		// Strict filename validation: only allow alphanumeric, dash, underscore, dot.
		if name == "" || !isValidOutputFilename(name) {
			http.Error(w, `{"error":"invalid filename"}`, http.StatusBadRequest)
			return
		}
		outputDir := filepath.Join(cfg.BaseDir, "outputs")
		filePath := filepath.Join(outputDir, name)
		// Verify resolved path is still within outputs dir (prevent symlink escape).
		absPath, err := filepath.Abs(filePath)
		if err != nil || !strings.HasPrefix(absPath, filepath.Join(cfg.BaseDir, "outputs")) {
			http.Error(w, `{"error":"invalid filename"}`, http.StatusBadRequest)
			return
		}
		data, err := os.ReadFile(absPath)
		if err != nil {
			if os.IsNotExist(err) {
				http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			} else {
				http.Error(w, `{"error":"read error"}`, http.StatusInternalServerError)
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	})

	// --- File Upload ---
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		cfg := s.Cfg()

		// Parse multipart form (max 50MB).
		if err := r.ParseMultipartForm(50 << 20); err != nil {
			jsonError(w, "parse form: "+err.Error(), http.StatusBadRequest)
			return
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			jsonError(w, "no file: "+err.Error(), http.StatusBadRequest)
			return
		}
		defer file.Close()

		uploadDir := upload.InitDir(cfg.BaseDir)
		uploaded, err := upload.Save(uploadDir, header.Filename, file, header.Size, "http")
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		audit.Log(cfg.HistoryDB, "file.upload", "http", uploaded.Name, clientIP(r))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(uploaded)
	})

	// --- Prompt Library ---
	mux.HandleFunc("/prompts", func(w http.ResponseWriter, r *http.Request) {
		cfg := s.Cfg()
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case "GET":
			prompts, err := listPrompts(cfg)
			if err != nil {
				jsonError(w, err.Error(), http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(prompts)

		case "POST":
			var body struct {
				Name    string `json:"name"`
				Content string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
				return
			}
			if body.Name == "" || body.Content == "" {
				http.Error(w, `{"error":"name and content are required"}`, http.StatusBadRequest)
				return
			}
			if err := writePrompt(cfg, body.Name, body.Content); err != nil {
				jsonError(w, err.Error(), http.StatusBadRequest)
				return
			}
			audit.Log(cfg.HistoryDB, "prompt.create", "http", body.Name, clientIP(r))
			json.NewEncoder(w).Encode(map[string]string{"status": "ok", "name": body.Name})

		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/prompts/", func(w http.ResponseWriter, r *http.Request) {
		cfg := s.Cfg()
		name := strings.TrimPrefix(r.URL.Path, "/prompts/")
		if name == "" {
			http.Error(w, `{"error":"prompt name required"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case "GET":
			content, err := readPrompt(cfg, name)
			if err != nil {
				jsonError(w, err.Error(), http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"name": name, "content": content})

		case "DELETE":
			if err := deletePrompt(cfg, name); err != nil {
				jsonError(w, err.Error(), http.StatusNotFound)
				return
			}
			audit.Log(cfg.HistoryDB, "prompt.delete", "http", name, clientIP(r))
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	})

	// --- Cost Estimate ---
	mux.HandleFunc("/dispatch/estimate", func(w http.ResponseWriter, r *http.Request) {
		cfg := s.Cfg()
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		var tasks []Task
		if err := json.NewDecoder(r.Body).Decode(&tasks); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		result := estimateTasks(cfg, tasks)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	})

	// --- Failed Tasks + Retry/Reroute ---
	mux.HandleFunc("/dispatch/failed", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		failedClientID := getClientID(r)
		failedState, _, _ := s.resolveClientDispatch(failedClientID)
		tasks := listFailedTasks(failedState)
		if tasks == nil {
			tasks = []failedTaskInfo{}
		}
		json.NewEncoder(w).Encode(tasks)
	})

	mux.HandleFunc("/dispatch/", func(w http.ResponseWriter, r *http.Request) {
		cfg := s.Cfg()
		// Parse /dispatch/{id}/{action}
		path := strings.TrimPrefix(r.URL.Path, "/dispatch/")
		if path == "failed" || path == "estimate" {
			return // handled by dedicated handlers
		}
		parts := strings.SplitN(path, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			http.Error(w, `{"error":"path must be /dispatch/{id}/{action}"}`, http.StatusBadRequest)
			return
		}
		taskID, action := parts[0], parts[1]

		// SSE stream endpoint: GET /dispatch/{id}/stream
		if action == "stream" && r.Method == http.MethodGet {
			streamClientID := getClientID(r)
			streamState, _, _ := s.resolveClientDispatch(streamClientID)
			if streamState.broker == nil {
				http.Error(w, `{"error":"streaming not available"}`, http.StatusServiceUnavailable)
				return
			}
			serveSSE(w, r, streamState.broker, taskID)
			return
		}

		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		actionClientID := getClientID(r)
		actionState, actionSem, actionChildSem := s.resolveClientDispatch(actionClientID)
		actionAuditDB := s.resolveHistoryDB(cfg, actionClientID)
		w.Header().Set("Content-Type", "application/json")

		// Decouple from HTTP request lifecycle so client disconnect doesn't kill in-flight tasks.
		actionCtx := trace.WithID(context.Background(), trace.IDFromContext(r.Context()))

		switch action {
		case "retry":
			result, err := retryTask(actionCtx, cfg, taskID, actionState, actionSem, actionChildSem)
			if err != nil {
				jsonError(w, err.Error(), http.StatusNotFound)
				return
			}
			audit.Log(actionAuditDB, "task.retry", "http",
				fmt.Sprintf("original=%s status=%s", taskID, result.Status), clientIP(r))
			json.NewEncoder(w).Encode(result)

		case "reroute":
			result, err := rerouteTask(actionCtx, cfg, taskID, actionState, actionSem, actionChildSem)
			if err != nil {
				status := http.StatusNotFound
				if strings.Contains(err.Error(), "not enabled") {
					status = http.StatusBadRequest
				}
				jsonError(w, err.Error(), status)
				return
			}
			audit.Log(actionAuditDB, "task.reroute", "http",
				fmt.Sprintf("original=%s role=%s status=%s", taskID, result.Route.Agent, result.Task.Status), clientIP(r))
			json.NewEncoder(w).Encode(result)

		default:
			http.Error(w, `{"error":"unknown action, use retry, reroute, or stream"}`, http.StatusBadRequest)
		}
	})

	// --- Smart Dispatch Route ---
	mux.HandleFunc("/route/classify", func(w http.ResponseWriter, r *http.Request) {
		cfg := s.Cfg()
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		if !cfg.SmartDispatch.Enabled {
			http.Error(w, `{"error":"smart dispatch not enabled"}`, http.StatusBadRequest)
			return
		}
		var body struct {
			Prompt string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Prompt == "" {
			http.Error(w, `{"error":"prompt is required"}`, http.StatusBadRequest)
			return
		}
		route := routeTask(r.Context(), cfg, RouteRequest{Prompt: body.Prompt, Source: "http"})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(route)
	})

	mux.HandleFunc("/route/", func(w http.ResponseWriter, r *http.Request) {
		// Handle /route/classify separately (already registered above, but paths
		// with trailing content after /route/ that aren't "classify" are async IDs).
		path := strings.TrimPrefix(r.URL.Path, "/route/")
		if path == "classify" {
			return // handled by /route/classify handler
		}

		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}

		// GET /route/{id} — check async route result.
		id := path
		if id == "" {
			http.Error(w, `{"error":"id required"}`, http.StatusBadRequest)
			return
		}

		routeResultsMu.Lock()
		entry, ok := routeResults[id]
		routeResultsMu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if !ok {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}

		json.NewEncoder(w).Encode(map[string]any{
			"id":        id,
			"status":    entry.Status,
			"error":     entry.Error,
			"result":    entry.Result,
			"createdAt": entry.CreatedAt.Format(time.RFC3339),
		})
	})

	mux.HandleFunc("/route", func(w http.ResponseWriter, r *http.Request) {
		cfg := s.Cfg()
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"enabled":     cfg.SmartDispatch.Enabled,
				"coordinator": cfg.SmartDispatch.Coordinator,
				"defaultAgent": cfg.SmartDispatch.DefaultAgent,
				"rules":       cfg.SmartDispatch.Rules,
			})
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"GET or POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		if !cfg.SmartDispatch.Enabled {
			http.Error(w, `{"error":"smart dispatch not enabled"}`, http.StatusBadRequest)
			return
		}
		var body struct {
			Prompt string `json:"prompt"`
			Async  bool   `json:"async"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Prompt == "" {
			http.Error(w, `{"error":"prompt is required"}`, http.StatusBadRequest)
			return
		}
		audit.Log(cfg.HistoryDB, "route.request", "http",
			truncate(body.Prompt, 100), clientIP(r))

		if body.Async {
			// Async mode: start in goroutine, return ID immediately.
			id := newUUID()

			routeResultsMu.Lock()
			routeResults[id] = &routeResultEntry{
				Status:    "running",
				CreatedAt: time.Now(),
			}
			routeResultsMu.Unlock()

			routeTraceID := trace.IDFromContext(r.Context())
			sdClientID := getClientID(r)
			sdState, sdSem, sdChildSem := s.resolveClientDispatch(sdClientID)
			go func() {
				routeCtx := trace.WithID(context.Background(), routeTraceID)
				result := smartDispatch(routeCtx, cfg, body.Prompt, "http", sdState, sdSem, sdChildSem)
				routeResultsMu.Lock()
				entry := routeResults[id]
				if entry != nil {
					entry.Result = result
					entry.Status = "done"
					if result != nil && result.Task.Status != "success" {
						entry.Error = result.Task.Error
					}
				}
				routeResultsMu.Unlock()
			}()

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]any{
				"id":     id,
				"status": "running",
			})
			return
		}

		// Sync mode: block until complete.
		syncClientID := getClientID(r)
		syncState, syncSem, syncChildSem := s.resolveClientDispatch(syncClientID)
		// Decouple from HTTP request lifecycle so client disconnect doesn't kill in-flight tasks.
		syncCtx := trace.WithID(context.Background(), trace.IDFromContext(r.Context()))
		result := smartDispatch(syncCtx, cfg, body.Prompt, "http", syncState, syncSem, syncChildSem)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	})
}

// ============================================================
// Merged from incoming_webhook.go
// ============================================================

// --- Incoming Webhook Types ---

// IncomingWebhookResult is the response from processing an incoming webhook.
type IncomingWebhookResult struct {
	Name     string `json:"name"`
	Status   string `json:"status"`   // "accepted", "filtered", "error", "disabled"
	TaskID   string `json:"taskId,omitempty"`
	Agent    string `json:"agent,omitempty"`
	Workflow string `json:"workflow,omitempty"`
	Message  string `json:"message,omitempty"`
}

// --- Signature Verification (delegated to internal/messaging/webhook) ---

// verifyWebhookSignature checks the request signature against the shared secret.
func verifyWebhookSignature(r *http.Request, body []byte, secret string) bool {
	return webhook.VerifySignature(r, body, secret)
}

// verifyHMACSHA256 checks HMAC-SHA256 signature.
func verifyHMACSHA256(body []byte, secret, signatureHex string) bool {
	return webhook.VerifyHMACSHA256(body, secret, signatureHex)
}

// --- Payload Template Expansion (delegated to internal/messaging/webhook) ---

// expandPayloadTemplate replaces {{payload.xxx}} placeholders with payload values.
func expandPayloadTemplate(tmpl string, payload map[string]any) string {
	return webhook.ExpandTemplate(tmpl, payload)
}

// getNestedValue retrieves a value from a nested map using dot notation.
func getNestedValue(m map[string]any, path string) any {
	return webhook.GetNestedValue(m, path)
}

// --- Filter Evaluation (delegated to internal/messaging/webhook) ---

// evaluateFilter checks if a payload matches a simple filter expression.
func evaluateFilter(filter string, payload map[string]any) bool {
	return webhook.EvaluateFilter(filter, payload)
}

func isTruthy(val any) bool {
	return webhook.IsTruthy(val)
}

// --- Webhook Handler ---

// handleIncomingWebhook processes an incoming webhook request.
func handleIncomingWebhook(ctx context.Context, cfg *Config, name string, r *http.Request,
	state *dispatchState, sem, childSem chan struct{}) IncomingWebhookResult {

	whCfg, ok := cfg.IncomingWebhooks[name]
	if !ok {
		return IncomingWebhookResult{
			Name: name, Status: "error",
			Message: fmt.Sprintf("webhook %q not found", name),
		}
	}

	if !whCfg.IsEnabled() {
		return IncomingWebhookResult{Name: name, Status: "disabled"}
	}

	// Read body.
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB max
	if err != nil {
		return IncomingWebhookResult{
			Name: name, Status: "error",
			Message: fmt.Sprintf("read body: %v", err),
		}
	}

	// Verify signature.
	if !verifyWebhookSignature(r, body, whCfg.Secret) {
		log.Warn("incoming webhook signature mismatch", "name", name)
		audit.Log(cfg.HistoryDB, "webhook.incoming.auth_fail", "http", name, clientIP(r))
		return IncomingWebhookResult{
			Name: name, Status: "error",
			Message: "signature verification failed",
		}
	}

	// Parse payload.
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return IncomingWebhookResult{
			Name: name, Status: "error",
			Message: fmt.Sprintf("parse payload: %v", err),
		}
	}

	// Apply filter.
	if !evaluateFilter(whCfg.Filter, payload) {
		log.DebugCtx(ctx, "incoming webhook filtered out", "name", name, "filter", whCfg.Filter)
		return IncomingWebhookResult{Name: name, Status: "filtered"}
	}

	// Build prompt from template.
	prompt := whCfg.Template
	if prompt != "" {
		prompt = expandPayloadTemplate(prompt, payload)
	} else {
		// Default: pretty-print the entire payload.
		b, _ := json.MarshalIndent(payload, "", "  ")
		prompt = fmt.Sprintf("Process this webhook event (%s):\n\n%s", name, string(b))
	}

	log.InfoCtx(ctx, "incoming webhook accepted", "name", name, "agent", whCfg.Agent)
	audit.Log(cfg.HistoryDB, "webhook.incoming", "http",
		fmt.Sprintf("name=%s agent=%s", name, whCfg.Agent), clientIP(r))

	// Trigger workflow or dispatch.
	if whCfg.Workflow != "" {
		return triggerWebhookWorkflow(ctx, cfg, name, whCfg, payload, prompt, state, sem, childSem)
	}
	return triggerWebhookDispatch(ctx, cfg, name, whCfg, prompt, state, sem, childSem)
}

// triggerWebhookDispatch dispatches a task to the specified agent.
func triggerWebhookDispatch(ctx context.Context, cfg *Config, name string, whCfg IncomingWebhookConfig,
	prompt string, state *dispatchState, sem, childSem chan struct{}) IncomingWebhookResult {

	task := Task{
		Prompt: prompt,
		Agent:   whCfg.Agent,
		Source: "webhook:" + name,
	}
	fillDefaults(cfg, &task)

	// Run async.
	bgCtx, bgCancel := context.WithTimeout(
		trace.WithID(context.Background(), trace.IDFromContext(ctx)),
		10*time.Minute,
	)
	go func() {
		defer bgCancel()
		result := runSingleTask(bgCtx, cfg, task, sem, childSem, whCfg.Agent)

		// Record history.
		start := time.Now().Add(-time.Duration(result.DurationMs) * time.Millisecond)
		recordHistoryCtx(bgCtx, cfg.HistoryDB, task.ID, task.Name, task.Source, whCfg.Agent, task, result,
			start.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)

		// Record session activity.
		recordSessionActivityCtx(bgCtx, cfg.HistoryDB, task, result, whCfg.Agent)

		log.InfoCtx(bgCtx, "incoming webhook task done", "name", name, "taskId", task.ID[:8],
			"status", result.Status, "cost", result.CostUSD)
	}()

	return IncomingWebhookResult{
		Name:   name,
		Status: "accepted",
		TaskID: task.ID,
		Agent:   whCfg.Agent,
	}
}

// triggerWebhookWorkflow loads and executes a workflow.
func triggerWebhookWorkflow(ctx context.Context, cfg *Config, name string, whCfg IncomingWebhookConfig,
	payload map[string]any, prompt string, state *dispatchState, sem, childSem chan struct{}) IncomingWebhookResult {

	wf, err := loadWorkflowByName(cfg, whCfg.Workflow)
	if err != nil {
		return IncomingWebhookResult{
			Name: name, Status: "error",
			Message: fmt.Sprintf("load workflow %q: %v", whCfg.Workflow, err),
		}
	}

	// Build workflow variables from payload.
	vars := map[string]string{
		"input":        prompt,
		"webhook_name": name,
	}
	// Flatten top-level payload keys as variables.
	for k, v := range payload {
		switch val := v.(type) {
		case string:
			vars["payload_"+k] = val
		case float64:
			if val == float64(int(val)) {
				vars["payload_"+k] = fmt.Sprintf("%d", int(val))
			} else {
				vars["payload_"+k] = fmt.Sprintf("%g", val)
			}
		case bool:
			vars["payload_"+k] = fmt.Sprintf("%v", val)
		}
	}

	// Run async.
	bgCtx, bgCancel := context.WithTimeout(
		trace.WithID(context.Background(), trace.IDFromContext(ctx)),
		10*time.Minute,
	)
	go func() {
		defer bgCancel()
		run := executeWorkflow(bgCtx, cfg, wf, vars, state, sem, childSem)
		log.InfoCtx(bgCtx, "incoming webhook workflow done", "name", name,
			"workflow", whCfg.Workflow, "status", run.Status, "cost", run.TotalCost)
	}()

	return IncomingWebhookResult{
		Name:     name,
		Status:   "accepted",
		Agent:     whCfg.Agent,
		Workflow: whCfg.Workflow,
	}
}

//go:embed docs/apidocs.html
var apiDocsHTML string

// handleAPIDocs serves the embedded API documentation HTML page.
func handleAPIDocs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte(apiDocsHTML))
}

// handleAPISpec returns the OpenAPI 3.0 spec as JSON, dynamically built from config.
func handleAPISpec(cfg *Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		spec := buildOpenAPISpec(cfg)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		json.NewEncoder(w).Encode(spec)
	}
}

// buildOpenAPISpec constructs a full OpenAPI 3.0.3 specification for the Tetora API.
func buildOpenAPISpec(cfg *Config) map[string]any {
	spec := map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":       "Tetora API",
			"description": "AI Agent Orchestrator API — zero-dependency multi-agent dispatch, session management, workflow orchestration, and cost governance.",
			"version":     tetoraVersion,
		},
		"servers": []map[string]any{
			{"url": "http://" + cfg.ListenAddr, "description": "Local"},
		},
		"paths":      buildPaths(),
		"components": buildComponents(),
		"tags":       buildTags(),
	}

	// Add security scheme if API token is configured.
	if cfg.APIToken != "" {
		spec["security"] = []map[string]any{{"bearerAuth": []string{}}}
	}

	return spec
}

// buildTags returns the tag definitions for grouping endpoints.
func buildTags() []map[string]any {
	return []map[string]any{
		{"name": "Core", "description": "Task dispatch, cancellation, and status"},
		{"name": "Health", "description": "Health check and diagnostics"},
		{"name": "History", "description": "Execution history and cost statistics"},
		{"name": "Sessions", "description": "Conversational session management"},
		{"name": "Workflows", "description": "Workflow definition and execution"},
		{"name": "Knowledge", "description": "Knowledge base files and search"},
		{"name": "Infrastructure", "description": "Circuit breakers, queue, budget, and SLA"},
		{"name": "Cron", "description": "Scheduled job management"},
		{"name": "Agent", "description": "Agent messages, handoffs, and reflections"},
		{"name": "Agents", "description": "Agent configuration"},
		{"name": "Stats", "description": "Cost and performance statistics"},
		{"name": "Audit", "description": "Audit log and backup"},
	}
}

// buildPaths constructs the paths section of the OpenAPI spec.
func buildPaths() map[string]any {
	paths := map[string]any{}

	// ---- Core ----

	paths["/dispatch"] = map[string]any{
		"post": opPost("Dispatch tasks", "Core",
			"Submit one or more tasks for concurrent execution. Each task is routed to the appropriate agent and provider.",
			reqBody(ref("TaskArray")),
			resp200(ref("DispatchResult")),
			resp400(), resp401(), resp409("dispatch already running"),
		),
	}

	paths["/dispatch/estimate"] = map[string]any{
		"post": opPost("Estimate dispatch cost", "Core",
			"Estimate the cost of running tasks without executing them.",
			reqBody(ref("TaskArray")),
			resp200(ref("EstimateResult")),
			resp400(), resp401(),
		),
	}

	paths["/dispatch/failed"] = map[string]any{
		"get": opGet("List failed tasks", "Core",
			"List recently failed tasks available for retry or reroute.",
			nil,
			resp200(schemaArray(ref("FailedTask"))),
			resp401(),
		),
	}

	paths["/dispatch/{taskId}"] = map[string]any{
		"post": opPost("Retry or reroute failed task", "Core",
			"Retry a failed task with original params or reroute to a different agent.",
			reqBody(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": prop("string", "Action to perform: retry or reroute"),
					"role":   prop("string", "Target agent for reroute (required if action=reroute)"),
				},
			}),
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"status": prop("string", ""),
				"taskId": prop("string", ""),
			}}),
			resp400(), resp401(), resp404(),
		),
	}

	paths["/cancel"] = map[string]any{
		"post": opPost("Cancel running dispatch", "Core",
			"Cancel all currently running tasks in the active dispatch.",
			nil,
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"status": prop("string", "cancelling or nothing to cancel"),
			}}),
			resp401(),
		),
	}

	paths["/cancel/{taskId}"] = map[string]any{
		"post": opPost("Cancel single task", "Core",
			"Cancel a specific running task by ID.",
			nil,
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"status": prop("string", ""),
			}}),
			resp401(), resp404(),
		),
	}

	paths["/tasks/running"] = map[string]any{
		"get": opGet("List running tasks", "Core",
			"List all currently running tasks from dispatch and cron.",
			nil,
			resp200(schemaArray(ref("RunningTask"))),
			resp401(),
		),
	}

	// ---- Health ----

	paths["/healthz"] = map[string]any{
		"get": opGet("Health check", "Health",
			"Deep health check including DB, providers, disk, and uptime status.",
			nil,
			resp200(ref("HealthResult")),
		),
	}

	// ---- History ----

	paths["/history"] = map[string]any{
		"get": opGet("List execution history", "History",
			"Query execution history with filtering and pagination.",
			[]map[string]any{
				queryParam("job_id", "string", "Filter by job ID"),
				queryParam("status", "string", "Filter by status (success, error, timeout)"),
				queryParam("from", "string", "Start date (RFC3339)"),
				queryParam("to", "string", "End date (RFC3339)"),
				queryParam("limit", "integer", "Results per page (default 20)"),
				queryParam("page", "integer", "Page number (default 1)"),
				queryParam("offset", "integer", "Offset (overrides page)"),
			},
			resp200(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"runs":  schemaArray(ref("JobRun")),
					"total": prop("integer", "Total matching records"),
					"page":  prop("integer", "Current page"),
					"limit": prop("integer", "Results per page"),
				},
			}),
			resp401(),
		),
	}

	paths["/history/{id}"] = map[string]any{
		"get": opGet("Get history entry", "History",
			"Get a single execution history entry by ID.",
			[]map[string]any{pathParam("id", "integer", "History entry ID")},
			resp200(ref("JobRun")),
			resp401(), resp404(),
		),
	}

	paths["/stats/cost"] = map[string]any{
		"get": opGet("Cost statistics", "Stats",
			"Get cost statistics summary (today, week, month, total).",
			nil,
			resp200(map[string]any{"type": "object"}),
			resp401(),
		),
	}

	paths["/stats/trend"] = map[string]any{
		"get": opGet("Cost trend", "Stats",
			"Get daily cost trend for the last N days.",
			[]map[string]any{queryParam("days", "integer", "Number of days (default 30)")},
			resp200(map[string]any{"type": "object"}),
			resp401(),
		),
	}

	paths["/stats/metrics"] = map[string]any{
		"get": opGet("Performance metrics", "Stats",
			"Get performance metrics (success rate, latency, throughput) per agent.",
			[]map[string]any{queryParam("days", "integer", "Number of days (default 7)")},
			resp200(map[string]any{"type": "object"}),
			resp401(),
		),
	}

	paths["/stats/routing"] = map[string]any{
		"get": opGet("Routing statistics", "Stats",
			"Get smart dispatch routing statistics by agent.",
			[]map[string]any{queryParam("days", "integer", "Number of days (default 7)")},
			resp200(map[string]any{"type": "object"}),
			resp401(),
		),
	}

	paths["/stats/sla"] = map[string]any{
		"get": opGet("SLA statistics", "Stats",
			"Get SLA metrics per agent (success rate, latency, cost).",
			[]map[string]any{
				queryParam("role", "string", "Filter by agent"),
				queryParam("days", "integer", "Window in days (default from SLA config)"),
			},
			resp200(schemaArray(ref("SLAMetrics"))),
			resp401(),
		),
	}

	// ---- Sessions ----

	paths["/sessions"] = map[string]any{
		"get": opGet("List sessions", "Sessions",
			"List conversational sessions with optional filtering.",
			[]map[string]any{
				queryParam("role", "string", "Filter by agent"),
				queryParam("status", "string", "Filter by status (active, archived)"),
				queryParam("source", "string", "Filter by source"),
				queryParam("limit", "integer", "Results per page (default 20)"),
				queryParam("offset", "integer", "Offset for pagination"),
			},
			resp200(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"sessions": schemaArray(ref("Session")),
					"total":    prop("integer", "Total matching sessions"),
				},
			}),
			resp401(),
		),
		"post": opPost("Create session", "Sessions",
			"Create a new conversational session.",
			reqBody(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"role":   prop("string", "Agent name"),
					"title":  prop("string", "Session title"),
					"source": prop("string", "Source identifier"),
				},
			}),
			resp200(ref("Session")),
			resp400(), resp401(),
		),
	}

	paths["/sessions/{id}"] = map[string]any{
		"get": opGet("Get session detail", "Sessions",
			"Get session metadata and message history.",
			[]map[string]any{pathParam("id", "string", "Session ID")},
			resp200(ref("SessionDetail")),
			resp401(), resp404(),
		),
		"delete": opDelete("Delete session", "Sessions",
			"Delete a session and its messages.",
			[]map[string]any{pathParam("id", "string", "Session ID")},
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"status": prop("string", "deleted"),
			}}),
			resp401(), resp404(),
		),
	}

	paths["/sessions/{id}/message"] = map[string]any{
		"post": opPost("Send message to session", "Sessions",
			"Send a user message to a session and get an assistant response.",
			reqBody(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"content": prop("string", "Message content"),
					"model":   prop("string", "Override model for this message"),
				},
				"required": []string{"content"},
			}),
			resp200(ref("SessionMessage")),
			resp400(), resp401(), resp404(),
		),
	}

	paths["/sessions/{id}/compact"] = map[string]any{
		"post": opPost("Compact session", "Sessions",
			"Compact session history to reduce token usage.",
			nil,
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"status":          prop("string", ""),
				"removedMessages": prop("integer", "Number of messages removed"),
			}}),
			resp401(), resp404(),
		),
	}

	paths["/sessions/{id}/stream"] = map[string]any{
		"get": opGet("Session SSE stream", "Sessions",
			"Server-Sent Events stream for real-time session updates.",
			[]map[string]any{pathParam("id", "string", "Session ID")},
			resp200(map[string]any{"type": "string", "description": "SSE event stream"}),
			resp401(), resp404(),
		),
	}

	// ---- Workflows ----

	paths["/workflows"] = map[string]any{
		"get": opGet("List workflows", "Workflows",
			"List all saved workflow definitions.",
			nil,
			resp200(schemaArray(ref("Workflow"))),
			resp401(),
		),
		"post": opPost("Create workflow", "Workflows",
			"Create or update a workflow definition.",
			reqBody(ref("Workflow")),
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"status": prop("string", ""),
				"name":   prop("string", "Workflow name"),
			}}),
			resp400(), resp401(),
		),
	}

	paths["/workflows/{name}"] = map[string]any{
		"get": opGet("Get workflow", "Workflows",
			"Get a single workflow definition by name.",
			[]map[string]any{pathParam("name", "string", "Workflow name")},
			resp200(ref("Workflow")),
			resp401(), resp404(),
		),
		"delete": opDelete("Delete workflow", "Workflows",
			"Delete a workflow definition.",
			[]map[string]any{pathParam("name", "string", "Workflow name")},
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"status": prop("string", "deleted"),
			}}),
			resp401(), resp404(),
		),
	}

	paths["/workflows/{name}/validate"] = map[string]any{
		"post": opPost("Validate workflow", "Workflows",
			"Validate a workflow definition (DAG, step references, etc.) without executing.",
			nil,
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"valid":  map[string]any{"type": "boolean"},
				"errors": schemaArray(prop("string", "")),
			}}),
			resp401(), resp404(),
		),
	}

	paths["/workflows/{name}/run"] = map[string]any{
		"post": opPost("Run workflow", "Workflows",
			"Execute a workflow. Supports live, dry-run, and shadow modes.",
			reqBody(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"variables": map[string]any{"type": "object", "additionalProperties": prop("string", "")},
					"mode":      prop("string", "Execution mode: live (default), dry-run, shadow"),
				},
			}),
			resp200(ref("WorkflowRun")),
			resp400(), resp401(), resp404(),
		),
	}

	paths["/workflow-runs"] = map[string]any{
		"get": opGet("List workflow runs", "Workflows",
			"List workflow execution runs with optional filtering.",
			[]map[string]any{
				queryParam("workflow", "string", "Filter by workflow name"),
			},
			resp200(schemaArray(ref("WorkflowRun"))),
			resp401(),
		),
	}

	paths["/workflow-runs/{id}"] = map[string]any{
		"get": opGet("Get workflow run", "Workflows",
			"Get workflow run details including step results, handoffs, and agent messages.",
			[]map[string]any{pathParam("id", "string", "Workflow run ID")},
			resp200(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run":      ref("WorkflowRun"),
					"handoffs": schemaArray(ref("Handoff")),
					"messages": schemaArray(ref("AgentMessage")),
				},
			}),
			resp401(), resp404(),
		),
	}

	// ---- Knowledge ----

	paths["/knowledge"] = map[string]any{
		"get": opGet("List knowledge files", "Knowledge",
			"List all files in the knowledge base directory.",
			nil,
			resp200(schemaArray(ref("KnowledgeFile"))),
			resp401(),
		),
	}

	paths["/knowledge/search"] = map[string]any{
		"get": opGet("Search knowledge base", "Knowledge",
			"TF-IDF search across knowledge base files.",
			[]map[string]any{
				queryParam("q", "string", "Search query"),
				queryParam("limit", "integer", "Max results (default 10)"),
			},
			resp200(schemaArray(ref("SearchResult"))),
			resp401(),
		),
	}

	// ---- Infrastructure ----

	paths["/circuits"] = map[string]any{
		"get": opGet("Circuit breaker status", "Infrastructure",
			"Get the current state of all provider circuit breakers.",
			nil,
			resp200(map[string]any{"type": "object", "description": "Map of provider name to circuit state (closed, open, half-open)"}),
			resp401(),
		),
	}

	paths["/circuits/{provider}/reset"] = map[string]any{
		"post": opPost("Reset circuit breaker", "Infrastructure",
			"Reset a provider's circuit breaker to closed state.",
			nil,
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"provider": prop("string", "Provider name"),
				"state":    prop("string", "New state (closed)"),
			}}),
			resp401(), resp404(),
		),
	}

	paths["/queue"] = map[string]any{
		"get": opGet("List offline queue", "Infrastructure",
			"List items in the offline task queue.",
			[]map[string]any{
				queryParam("status", "string", "Filter by status (pending, processing, completed, expired, failed)"),
			},
			resp200(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"items":   schemaArray(ref("QueueItem")),
					"count":   prop("integer", "Number of items returned"),
					"pending": prop("integer", "Total pending items"),
				},
			}),
			resp401(),
		),
	}

	paths["/queue/{id}"] = map[string]any{
		"get": opGet("Get queue item", "Infrastructure",
			"Get a single offline queue item by ID.",
			[]map[string]any{pathParam("id", "integer", "Queue item ID")},
			resp200(ref("QueueItem")),
			resp401(), resp404(),
		),
		"delete": opDelete("Delete queue item", "Infrastructure",
			"Remove an item from the offline queue.",
			[]map[string]any{pathParam("id", "integer", "Queue item ID")},
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"status": prop("string", "deleted"),
			}}),
			resp401(), resp404(),
		),
	}

	paths["/queue/{id}/retry"] = map[string]any{
		"post": opPost("Retry queue item", "Infrastructure",
			"Retry a pending or failed queue item.",
			nil,
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"status": prop("string", "retrying"),
				"taskId": prop("string", "New task ID"),
			}}),
			resp401(), resp404(), resp409("item not in retryable state"),
		),
	}

	paths["/budget"] = map[string]any{
		"get": opGet("Budget status", "Infrastructure",
			"Get current budget utilization across global, agent, and workflow scopes.",
			nil,
			resp200(map[string]any{"type": "object", "description": "Budget status with daily/weekly/monthly usage and caps"}),
			resp401(),
		),
	}

	paths["/budget/pause"] = map[string]any{
		"post": opPost("Pause all paid execution", "Infrastructure",
			"Activate the kill switch to pause all paid LLM execution.",
			nil,
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"status": prop("string", "paused"),
			}}),
			resp401(),
		),
	}

	paths["/budget/resume"] = map[string]any{
		"post": opPost("Resume paid execution", "Infrastructure",
			"Deactivate the kill switch and resume paid LLM execution.",
			nil,
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"status": prop("string", "active"),
			}}),
			resp401(),
		),
	}

	// ---- Cron ----

	paths["/cron"] = map[string]any{
		"get": opGet("List cron jobs", "Cron",
			"List all configured cron jobs with their status and schedule.",
			nil,
			resp200(schemaArray(ref("CronJob"))),
			resp401(),
		),
	}

	paths["/cron/{id}/trigger"] = map[string]any{
		"post": opPost("Trigger cron job", "Cron",
			"Manually trigger a cron job to run immediately.",
			nil,
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"status": prop("string", "triggered"),
			}}),
			resp401(), resp404(),
		),
	}

	// ---- Agent ----

	paths["/agent-messages"] = map[string]any{
		"get": opGet("List agent messages", "Agent",
			"List inter-agent communication messages.",
			[]map[string]any{
				queryParam("workflowRun", "string", "Filter by workflow run ID"),
				queryParam("role", "string", "Filter by agent"),
				queryParam("limit", "integer", "Max results (default 50)"),
			},
			resp200(schemaArray(ref("AgentMessage"))),
			resp401(),
		),
		"post": opPost("Send agent message", "Agent",
			"Send an inter-agent communication message.",
			reqBody(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"fromAgent":      prop("string", "Sender agent"),
					"toAgent":        prop("string", "Recipient agent"),
					"type":          prop("string", "Message type: handoff, request, response, note"),
					"content":       prop("string", "Message content"),
					"workflowRunId": prop("string", "Associated workflow run ID"),
					"refId":         prop("string", "Reference to another message ID"),
				},
				"required": []string{"fromAgent", "toAgent", "content"},
			}),
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"status": prop("string", "sent"),
				"id":     prop("string", "Message ID"),
			}}),
			resp400(), resp401(),
		),
	}

	paths["/handoffs"] = map[string]any{
		"get": opGet("List handoffs", "Agent",
			"List agent handoffs with optional workflow run filter.",
			[]map[string]any{
				queryParam("workflowRun", "string", "Filter by workflow run ID"),
			},
			resp200(schemaArray(ref("Handoff"))),
			resp401(),
		),
	}

	// ---- Agents ----

	paths["/roles"] = map[string]any{
		"get": opGet("List agents", "Agents",
			"List all configured agents.",
			nil,
			resp200(map[string]any{"type": "object", "additionalProperties": ref("AgentConfig")}),
			resp401(),
		),
		"post": opPost("Create agent", "Agents",
			"Create or update an agent configuration.",
			reqBody(ref("AgentConfig")),
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"status": prop("string", ""),
				"name":   prop("string", "Agent name"),
			}}),
			resp400(), resp401(),
		),
	}

	paths["/roles/{name}"] = map[string]any{
		"get": opGet("Get agent", "Agents",
			"Get a single agent configuration by name.",
			[]map[string]any{pathParam("name", "string", "Agent name")},
			resp200(ref("AgentConfig")),
			resp401(), resp404(),
		),
		"put": map[string]any{
			"tags":        []string{"Agents"},
			"summary":     "Update agent",
			"description": "Update an existing agent configuration.",
			"parameters":  []map[string]any{pathParam("name", "string", "Agent name")},
			"requestBody": reqBody(ref("AgentConfig")),
			"responses": mergeResponses(
				resp200(map[string]any{"type": "object", "properties": map[string]any{
					"status": prop("string", ""),
					"name":   prop("string", "Agent name"),
				}}),
				resp400(), resp401(), resp404(),
			),
		},
		"delete": opDelete("Delete agent", "Agents",
			"Delete an agent.",
			[]map[string]any{pathParam("name", "string", "Agent name")},
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"status": prop("string", "deleted"),
			}}),
			resp401(), resp404(),
		),
	}

	paths["/roles/archetypes"] = map[string]any{
		"get": opGet("List agent archetypes", "Agents",
			"List available agent archetype templates.",
			nil,
			resp200(map[string]any{"type": "object"}),
			resp401(),
		),
	}

	paths["/api/agents/running"] = map[string]any{
		"get": opGet("List running agents", "Agents",
			"Return all tasks currently executing in dispatchState.",
			nil,
			resp200(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"running": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"id":       prop("string", "Task ID"),
								"name":     prop("string", "Task name"),
								"agent":    prop("string", "Agent name"),
								"source":   prop("string", "Source identifier"),
								"prompt":   prop("string", "Prompt (truncated to 100 chars)"),
								"elapsed":  prop("string", "Elapsed time (e.g. 5s)"),
								"parentId": prop("string", "Parent task ID (sub-tasks only)"),
								"depth":    map[string]any{"type": "integer", "description": "Nesting depth (0 = top-level)"},
							},
						},
					},
					"count": map[string]any{"type": "integer", "description": "Number of running tasks"},
				},
			}),
			resp401(),
		),
	}

	// ---- Route (Smart Dispatch) ----

	paths["/route"] = map[string]any{
		"post": opPost("Smart dispatch", "Core",
			"Send a natural language prompt for intelligent routing to the best agent.",
			reqBody(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt": prop("string", "Natural language prompt"),
					"async":  map[string]any{"type": "boolean", "description": "Run asynchronously (returns request ID)"},
				},
				"required": []string{"prompt"},
			}),
			resp200(ref("SmartDispatchResult")),
			resp400(), resp401(),
		),
	}

	paths["/route/classify"] = map[string]any{
		"post": opPost("Classify prompt", "Core",
			"Classify a prompt to determine which agent would handle it, without executing.",
			reqBody(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt": prop("string", "Natural language prompt"),
				},
				"required": []string{"prompt"},
			}),
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"role":       prop("string", "Matched agent"),
				"confidence": prop("string", "Confidence level"),
				"method":     prop("string", "Classification method (keyword, llm)"),
			}}),
			resp400(), resp401(),
		),
	}

	paths["/route/{id}"] = map[string]any{
		"get": opGet("Get async route result", "Core",
			"Poll the result of an asynchronous smart dispatch request.",
			[]map[string]any{pathParam("id", "string", "Request ID from async route")},
			resp200(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"status": prop("string", "running, done, or error"),
					"result": ref("SmartDispatchResult"),
					"error":  prop("string", "Error message if failed"),
				},
			}),
			resp401(), resp404(),
		),
	}

	// ---- Audit / Backup ----

	paths["/audit"] = map[string]any{
		"get": opGet("Audit log", "Audit",
			"Query the audit log with pagination.",
			[]map[string]any{
				queryParam("limit", "integer", "Results per page (default 50)"),
				queryParam("page", "integer", "Page number (default 1)"),
			},
			resp200(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"entries": schemaArray(ref("AuditEntry")),
					"total":   prop("integer", "Total entries"),
					"page":    prop("integer", "Current page"),
					"limit":   prop("integer", "Results per page"),
				},
			}),
			resp401(),
		),
	}

	// --- Retention & Data ---

	paths["/retention"] = map[string]any{
		"get": opGet("Get retention config & stats", "Data",
			"Returns the retention configuration (effective days per table) and current row counts.",
			nil,
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"config":   map[string]any{"type": "object", "description": "Configured retention days"},
				"defaults": map[string]any{"type": "object", "description": "Effective retention days (config or fallback)"},
				"stats":    map[string]any{"type": "object", "description": "Row count per table"},
			}}),
			resp401(),
		),
	}

	paths["/retention/cleanup"] = map[string]any{
		"post": opPost("Run retention cleanup", "Data",
			"Triggers an immediate retention cleanup across all tables.",
			nil,
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"results": schemaArray(map[string]any{"type": "object", "properties": map[string]any{
					"table": prop("string", "Table name"), "deleted": prop("integer", "Rows deleted"),
					"error": prop("string", "Error message if any"),
				}}),
			}}),
			resp401(),
		),
	}

	paths["/data/export"] = map[string]any{
		"get": opGet("Export all data", "Data",
			"Exports all user data as JSON (GDPR right of access). Includes history, sessions, memory, audit log, and reflections.",
			nil,
			resp200(map[string]any{"type": "object", "description": "Full data export"}),
			resp401(),
		),
	}

	paths["/data/purge"] = map[string]any{
		"delete": opDelete("Purge data before date", "Data",
			"Permanently deletes all data before the specified date. Requires X-Confirm-Purge: true header.",
			[]map[string]any{{
				"name": "before", "in": "query", "required": true,
				"description": "Date cutoff (YYYY-MM-DD)",
				"schema":      map[string]any{"type": "string", "format": "date"},
			}},
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"results": schemaArray(map[string]any{"type": "object"}),
			}}),
			resp401(),
		),
	}

	paths["/backup"] = map[string]any{
		"get": opGet("Download backup", "Audit",
			"Download a tar.gz backup of the Tetora data directory.",
			nil,
			map[string]any{
				"200": map[string]any{
					"description": "Backup archive",
					"content": map[string]any{
						"application/gzip": map[string]any{
							"schema": map[string]any{"type": "string", "format": "binary"},
						},
					},
				},
			},
			resp401(),
		),
	}

	return paths
}

// buildComponents constructs the components/schemas section.
func buildComponents() map[string]any {
	schemas := map[string]any{}

	schemas["Task"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":             prop("string", "Task ID (auto-generated if empty)"),
			"name":           prop("string", "Human-readable task name"),
			"prompt":         prop("string", "Task prompt for the agent"),
			"workdir":        prop("string", "Working directory"),
			"model":          prop("string", "LLM model to use"),
			"provider":       prop("string", "Provider name override"),
			"docker":         map[string]any{"type": "boolean", "description": "Run in Docker sandbox"},
			"timeout":        prop("string", "Timeout duration (e.g. 5m, 1h)"),
			"budget":         prop("number", "Max cost in USD"),
			"permissionMode": prop("string", "Permission mode for the agent"),
			"mcp":            prop("string", "MCP config name"),
			"addDirs":        schemaArray(prop("string", "")),
			"systemPrompt":   prop("string", "System prompt override"),
			"sessionId":      prop("string", "Session ID to continue"),
			"role":           prop("string", "Agent name"),
			"source":         prop("string", "Request source identifier"),
		},
		"required": []string{"prompt"},
	}

	schemas["TaskArray"] = schemaArray(ref("Task"))

	schemas["TaskResult"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":         prop("string", "Task ID"),
			"name":       prop("string", "Task name"),
			"status":     prop("string", "Execution status: success, error, timeout, cancelled"),
			"exitCode":   prop("integer", "Process exit code"),
			"output":     prop("string", "Agent output text"),
			"error":      prop("string", "Error message if failed"),
			"durationMs": prop("integer", "Execution duration in milliseconds"),
			"costUsd":    prop("number", "Actual cost in USD"),
			"model":      prop("string", "Model used"),
			"sessionId":  prop("string", "Session ID"),
			"outputFile": prop("string", "Output file path (if any)"),
			"tokensIn":   prop("integer", "Input tokens consumed"),
			"tokensOut":  prop("integer", "Output tokens generated"),
			"providerMs": prop("integer", "Provider-side latency in ms"),
			"traceId":    prop("string", "Trace ID for request correlation"),
			"provider":   prop("string", "Provider used"),
		},
	}

	schemas["DispatchResult"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"startedAt":    prop("string", "Start time (RFC3339)"),
			"finishedAt":   prop("string", "Finish time (RFC3339)"),
			"durationMs":   prop("integer", "Total duration in milliseconds"),
			"totalCostUsd": prop("number", "Total cost in USD"),
			"tasks":        schemaArray(ref("TaskResult")),
			"summary":      prop("string", "Execution summary"),
		},
	}

	schemas["EstimateResult"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tasks":                 schemaArray(ref("CostEstimate")),
			"totalEstimatedCostUsd": prop("number", "Total estimated cost"),
			"classifyCostUsd":       prop("number", "Cost of LLM classification (if smart dispatch)"),
		},
	}

	schemas["CostEstimate"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":               prop("string", "Task name"),
			"provider":           prop("string", "Resolved provider"),
			"model":              prop("string", "Resolved model"),
			"estimatedCostUsd":   prop("number", "Estimated cost in USD"),
			"estimatedTokensIn":  prop("integer", "Estimated input tokens"),
			"estimatedTokensOut": prop("integer", "Estimated output tokens"),
			"breakdown":          prop("string", "Cost breakdown description"),
		},
	}

	schemas["Session"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":             prop("string", "Session ID"),
			"role":           prop("string", "Agent name"),
			"source":         prop("string", "Source channel"),
			"status":         prop("string", "Status: active, archived"),
			"title":          prop("string", "Session title"),
			"channelKey":     prop("string", "Channel session key"),
			"totalCost":      prop("number", "Total cost for session"),
			"totalTokensIn":  prop("integer", "Total input tokens"),
			"totalTokensOut": prop("integer", "Total output tokens"),
			"messageCount":   prop("integer", "Number of messages"),
			"createdAt":      prop("string", "Created timestamp (RFC3339)"),
			"updatedAt":      prop("string", "Updated timestamp (RFC3339)"),
		},
	}

	schemas["SessionMessage"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":        prop("integer", "Message ID"),
			"sessionId": prop("string", "Session ID"),
			"role":      prop("string", "Message role: user, assistant, system"),
			"content":   prop("string", "Message content"),
			"costUsd":   prop("number", "Cost for this message"),
			"tokensIn":  prop("integer", "Input tokens"),
			"tokensOut": prop("integer", "Output tokens"),
			"model":     prop("string", "Model used"),
			"taskId":    prop("string", "Associated task ID"),
			"createdAt": prop("string", "Timestamp (RFC3339)"),
		},
	}

	schemas["SessionDetail"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"session":  ref("Session"),
			"messages": schemaArray(ref("SessionMessage")),
		},
	}

	schemas["Workflow"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":        prop("string", "Workflow name (unique identifier)"),
			"description": prop("string", "Human-readable description"),
			"steps":       schemaArray(ref("WorkflowStep")),
			"variables":   map[string]any{"type": "object", "additionalProperties": prop("string", ""), "description": "Input variables with default values"},
			"timeout":     prop("string", "Overall workflow timeout (e.g. 30m)"),
			"onSuccess":   prop("string", "Notification template on success"),
			"onFailure":   prop("string", "Notification template on failure"),
		},
		"required": []string{"name", "steps"},
	}

	schemas["WorkflowStep"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":             prop("string", "Step ID (unique within workflow)"),
			"type":           prop("string", "Step type: dispatch, skill, condition, parallel"),
			"role":           prop("string", "Agent name for dispatch steps"),
			"prompt":         prop("string", "Prompt for dispatch steps (supports {{variable}} substitution)"),
			"skill":          prop("string", "Skill name for skill steps"),
			"skillArgs":      schemaArray(prop("string", "")),
			"dependsOn":      schemaArray(prop("string", "Step IDs that must complete first")),
			"model":          prop("string", "Model override"),
			"provider":       prop("string", "Provider override"),
			"timeout":        prop("string", "Per-step timeout"),
			"budget":         prop("number", "Per-step budget cap"),
			"permissionMode": prop("string", "Permission mode override"),
			"if":             prop("string", "Condition expression"),
			"then":           prop("string", "Step ID on condition true"),
			"else":           prop("string", "Step ID on condition false"),
			"handoffFrom":    prop("string", "Source step ID whose output becomes context"),
			"parallel":       schemaArray(ref("WorkflowStep")),
			"retryMax":       prop("integer", "Max retries on failure"),
			"retryDelay":     prop("string", "Delay between retries"),
			"onError":        prop("string", "Error handling: stop (default), skip, retry"),
		},
		"required": []string{"id"},
	}

	schemas["WorkflowRun"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":           prop("string", "Run ID"),
			"workflowName": prop("string", "Workflow name"),
			"status":       prop("string", "Status: running, success, error, cancelled, timeout"),
			"startedAt":    prop("string", "Start time (RFC3339)"),
			"finishedAt":   prop("string", "Finish time (RFC3339)"),
			"durationMs":   prop("integer", "Duration in milliseconds"),
			"totalCostUsd": prop("number", "Total cost in USD"),
			"variables":    map[string]any{"type": "object", "additionalProperties": prop("string", "")},
			"stepResults":  map[string]any{"type": "object", "additionalProperties": ref("StepRunResult")},
			"error":        prop("string", "Error message if failed"),
		},
	}

	schemas["StepRunResult"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"stepId":     prop("string", "Step ID"),
			"status":     prop("string", "Status: pending, running, success, error, skipped, timeout"),
			"output":     prop("string", "Step output"),
			"error":      prop("string", "Error message"),
			"startedAt":  prop("string", "Start time"),
			"finishedAt": prop("string", "Finish time"),
			"durationMs": prop("integer", "Duration in ms"),
			"costUsd":    prop("number", "Cost in USD"),
			"taskId":     prop("string", "Task ID"),
			"sessionId":  prop("string", "Session ID"),
			"retries":    prop("integer", "Number of retries"),
		},
	}

	schemas["KnowledgeFile"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":    prop("string", "Filename"),
			"size":    prop("integer", "File size in bytes"),
			"modTime": prop("string", "Last modification time (RFC3339)"),
		},
	}

	schemas["SearchResult"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"filename":  prop("string", "Source filename"),
			"snippet":   prop("string", "Matched text snippet"),
			"score":     prop("number", "Relevance score"),
			"lineStart": prop("integer", "Starting line number of snippet"),
		},
	}

	schemas["Handoff"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":            prop("string", "Handoff ID"),
			"workflowRunId": prop("string", "Workflow run ID"),
			"fromAgent":      prop("string", "Source agent name"),
			"toAgent":        prop("string", "Target agent name"),
			"fromStepId":    prop("string", "Source step ID"),
			"toStepId":      prop("string", "Target step ID"),
			"fromSessionId": prop("string", "Source session ID"),
			"toSessionId":   prop("string", "Target session ID"),
			"context":       prop("string", "Output from source agent"),
			"instruction":   prop("string", "Instructions for target agent"),
			"status":        prop("string", "Status: pending, active, completed, error"),
			"createdAt":     prop("string", "Timestamp (RFC3339)"),
		},
	}

	schemas["AgentMessage"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":            prop("string", "Message ID"),
			"workflowRunId": prop("string", "Workflow run ID"),
			"fromAgent":      prop("string", "Sender agent"),
			"toAgent":        prop("string", "Recipient agent"),
			"type":          prop("string", "Message type: handoff, request, response, note"),
			"content":       prop("string", "Message content"),
			"refId":         prop("string", "Reference to another message"),
			"createdAt":     prop("string", "Timestamp (RFC3339)"),
		},
	}

	schemas["QueueItem"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":         prop("integer", "Queue item ID"),
			"taskJson":   prop("string", "Serialized task JSON"),
			"role":       prop("string", "Target agent"),
			"source":     prop("string", "Source identifier"),
			"priority":   prop("integer", "Priority (higher = sooner)"),
			"status":     prop("string", "Status: pending, processing, completed, expired, failed"),
			"retryCount": prop("integer", "Number of retries"),
			"createdAt":  prop("string", "Created timestamp (RFC3339)"),
			"updatedAt":  prop("string", "Updated timestamp (RFC3339)"),
			"error":      prop("string", "Error message"),
		},
	}

	schemas["SLAMetrics"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"role":         prop("string", "Agent name"),
			"total":        prop("integer", "Total executions"),
			"success":      prop("integer", "Successful executions"),
			"fail":         prop("integer", "Failed executions"),
			"successRate":  prop("number", "Success rate (0.0-1.0)"),
			"avgLatencyMs": prop("integer", "Average latency in ms"),
			"p95LatencyMs": prop("integer", "P95 latency in ms"),
			"totalCost":    prop("number", "Total cost in USD"),
			"avgCost":      prop("number", "Average cost per execution"),
		},
	}

	schemas["HealthResult"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"status":    prop("string", "Overall status: ok, degraded, error"),
			"version":   prop("string", "Tetora version"),
			"uptime":    prop("string", "Server uptime"),
			"uptimeSec": prop("integer", "Server uptime in seconds"),
			"db":        map[string]any{"type": "object", "description": "Database health details"},
			"providers": map[string]any{"type": "object", "description": "Provider availability"},
			"disk":      map[string]any{"type": "object", "description": "Disk usage information"},
			"cron":      map[string]any{"type": "object", "description": "Cron engine status"},
		},
	}

	schemas["CronJob"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":       prop("string", "Job ID"),
			"name":     prop("string", "Job name"),
			"schedule": prop("string", "Cron schedule expression"),
			"role":     prop("string", "Agent name"),
			"enabled":  map[string]any{"type": "boolean", "description": "Whether job is active"},
			"running":  map[string]any{"type": "boolean", "description": "Whether job is currently running"},
			"lastRun":  prop("string", "Last run timestamp"),
			"nextRun":  prop("string", "Next scheduled run"),
		},
	}

	schemas["AgentConfig"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":              prop("string", "Agent name"),
			"soulFile":          prop("string", "System prompt file path"),
			"model":             prop("string", "Default model for this agent"),
			"description":       prop("string", "Agent description"),
			"keywords":          schemaArray(prop("string", "Routing keywords")),
			"permissionMode":    prop("string", "Permission mode"),
			"allowedDirs":       schemaArray(prop("string", "Allowed directories")),
			"provider":          prop("string", "Preferred provider"),
			"docker":            map[string]any{"type": "boolean", "description": "Docker sandbox override"},
			"fallbackProviders": schemaArray(prop("string", "Failover chain")),
		},
	}

	schemas["SmartDispatchResult"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"role":       prop("string", "Selected agent name"),
			"method":     prop("string", "Classification method (keyword, llm)"),
			"confidence": prop("string", "Classification confidence"),
			"taskResult": ref("TaskResult"),
		},
	}

	schemas["FailedTask"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":       prop("string", "Task ID"),
			"name":     prop("string", "Task name"),
			"role":     prop("string", "Original agent"),
			"error":    prop("string", "Failure error message"),
			"failedAt": prop("string", "Failure timestamp (RFC3339)"),
		},
	}

	schemas["RunningTask"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":       prop("string", "Task ID"),
			"name":     prop("string", "Task name"),
			"source":   prop("string", "Source (dispatch, cron)"),
			"model":    prop("string", "Model in use"),
			"timeout":  prop("string", "Timeout setting"),
			"elapsed":  prop("string", "Elapsed time"),
			"prompt":   prop("string", "Prompt (truncated)"),
			"pid":      prop("integer", "Process ID"),
			"pidAlive": map[string]any{"type": "boolean", "description": "Whether process is alive"},
		},
	}

	schemas["JobRun"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":         prop("integer", "Record ID"),
			"taskId":     prop("string", "Task ID"),
			"jobName":    prop("string", "Job/task name"),
			"source":     prop("string", "Source identifier"),
			"role":       prop("string", "Agent name"),
			"status":     prop("string", "Execution status"),
			"model":      prop("string", "Model used"),
			"provider":   prop("string", "Provider used"),
			"costUsd":    prop("number", "Cost in USD"),
			"durationMs": prop("integer", "Duration in ms"),
			"tokensIn":   prop("integer", "Input tokens"),
			"tokensOut":  prop("integer", "Output tokens"),
			"startedAt":  prop("string", "Start time (RFC3339)"),
			"finishedAt": prop("string", "Finish time (RFC3339)"),
			"outputFile": prop("string", "Output file path"),
			"error":      prop("string", "Error message"),
		},
	}

	schemas["AuditEntry"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":        prop("integer", "Entry ID"),
			"event":     prop("string", "Event type"),
			"source":    prop("string", "Source"),
			"detail":    prop("string", "Event detail"),
			"ip":        prop("string", "Client IP"),
			"createdAt": prop("string", "Timestamp (RFC3339)"),
		},
	}

	schemas["Error"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"error": prop("string", "Error message"),
		},
		"required": []string{"error"},
	}

	components := map[string]any{
		"schemas": schemas,
	}

	// Security scheme (always defined, applied conditionally in spec root).
	components["securitySchemes"] = map[string]any{
		"bearerAuth": map[string]any{
			"type":         "http",
			"scheme":       "bearer",
			"bearerFormat": "token",
			"description":  "API token configured in config.json (apiToken field)",
		},
	}

	return components
}

// --- OpenAPI builder helpers ---

// prop creates a simple property schema.
func prop(typeName, description string) map[string]any {
	p := map[string]any{"type": typeName}
	if description != "" {
		p["description"] = description
	}
	return p
}

// ref creates a $ref to a component schema.
func ref(name string) map[string]any {
	return map[string]any{"$ref": "#/components/schemas/" + name}
}

// schemaArray creates an array schema wrapping an item schema.
func schemaArray(items map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": items}
}

// queryParam creates a query parameter definition.
func queryParam(name, typeName, description string) map[string]any {
	return map[string]any{
		"name":        name,
		"in":          "query",
		"required":    false,
		"description": description,
		"schema":      map[string]any{"type": typeName},
	}
}

// pathParam creates a path parameter definition.
func pathParam(name, typeName, description string) map[string]any {
	return map[string]any{
		"name":        name,
		"in":          "path",
		"required":    true,
		"description": description,
		"schema":      map[string]any{"type": typeName},
	}
}

// reqBody creates a requestBody definition with JSON content type.
// Pass nil for endpoints with no body.
func reqBody(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	return map[string]any{
		"required": true,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": schema,
			},
		},
	}
}

// resp200 creates a 200 response with a JSON schema.
func resp200(schema map[string]any) map[string]any {
	return map[string]any{
		"200": map[string]any{
			"description": "Success",
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": schema,
				},
			},
		},
	}
}

// resp400 creates a 400 Bad Request response.
func resp400() map[string]any {
	return map[string]any{
		"400": map[string]any{
			"description": "Bad Request",
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": ref("Error"),
				},
			},
		},
	}
}

// resp401 creates a 401 Unauthorized response.
func resp401() map[string]any {
	return map[string]any{
		"401": map[string]any{
			"description": "Unauthorized",
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": ref("Error"),
				},
			},
		},
	}
}

// resp404 creates a 404 Not Found response.
func resp404() map[string]any {
	return map[string]any{
		"404": map[string]any{
			"description": "Not Found",
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": ref("Error"),
				},
			},
		},
	}
}

// resp409 creates a 409 Conflict response.
func resp409(detail string) map[string]any {
	desc := "Conflict"
	if detail != "" {
		desc = "Conflict: " + detail
	}
	return map[string]any{
		"409": map[string]any{
			"description": desc,
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": ref("Error"),
				},
			},
		},
	}
}

// mergeResponses combines multiple response maps into one.
func mergeResponses(maps ...map[string]any) map[string]any {
	merged := map[string]any{}
	for _, m := range maps {
		for k, v := range m {
			merged[k] = v
		}
	}
	return merged
}

// opGet creates a GET operation definition.
func opGet(summary, tag, description string, params []map[string]any, responses ...map[string]any) map[string]any {
	op := map[string]any{
		"tags":        []string{tag},
		"summary":     summary,
		"description": description,
		"responses":   mergeResponses(responses...),
	}
	if len(params) > 0 {
		op["parameters"] = params
	}
	return op
}

// opPost creates a POST operation definition.
func opPost(summary, tag, description string, body map[string]any, responses ...map[string]any) map[string]any {
	op := map[string]any{
		"tags":        []string{tag},
		"summary":     summary,
		"description": description,
		"responses":   mergeResponses(responses...),
	}
	if body != nil {
		op["requestBody"] = body
	}
	return op
}

// opDelete creates a DELETE operation definition.
func opDelete(summary, tag, description string, params []map[string]any, responses ...map[string]any) map[string]any {
	op := map[string]any{
		"tags":        []string{tag},
		"summary":     summary,
		"description": description,
		"responses":   mergeResponses(responses...),
	}
	if len(params) > 0 {
		op["parameters"] = params
	}
	return op
}
