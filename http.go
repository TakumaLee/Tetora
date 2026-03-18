package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"tetora/internal/audit"
	"tetora/internal/cli"
	"tetora/internal/cost"
	"tetora/internal/history"
	"tetora/internal/httpapi"
	"tetora/internal/httputil"
	"tetora/internal/knowledge"
	"tetora/internal/log"
	"tetora/internal/pairing"
	"tetora/internal/pwa"
	"tetora/internal/quickaction"
	"tetora/internal/sla"
	"tetora/internal/store"
	"tetora/internal/trace"
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
		if p == "/healthz" || p == "/metrics" || p == "/dashboard" || strings.HasPrefix(p, "/dashboard/") || p == "/slack/events" || p == "/api/whatsapp/webhook" || p == "/api/discord/interactions" || strings.HasPrefix(p, "/api/line/") || strings.HasPrefix(p, "/api/teams/") || strings.HasPrefix(p, "/api/signal/") || strings.HasPrefix(p, "/api/gchat/") || strings.HasPrefix(p, "/api/imessage/") || p == "/api/docs" || p == "/api/spec" || strings.HasPrefix(p, "/hooks/") || isHooksPath(p) || (strings.HasPrefix(p, "/api/oauth/") && strings.HasSuffix(p, "/callback")) || strings.HasPrefix(p, "/api/callbacks/") {
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
	httpapi.RegisterHistoryRoutes(mux, func() string { return s.Cfg().HistoryDB })
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
		if err := taskBoardEngine.initTaskBoardSchema(); err != nil {
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

			// Sync mode.
			result := runSingleTask(r.Context(), cfg, task, s.sem, s.childSem, sess.Agent)
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
		STTEnabled:       s.voiceEngine != nil && s.voiceEngine.stt != nil,
		TTSEnabled:       s.voiceEngine != nil && s.voiceEngine.tts != nil,
		WakeEnabled:      cfg.Voice.Wake.Enabled,
		RealtimeEnabled:  cfg.Voice.Realtime.Enabled,
		DefaultTTSFormat: cfg.Voice.TTS.Format,
		Transcribe: func(ctx context.Context, audio io.Reader, opts httpapi.VoiceTranscribeOpts) (any, error) {
			return s.voiceEngine.Transcribe(ctx, audio, STTOptions{Language: opts.Language, Format: opts.Format})
		},
		Synthesize: func(ctx context.Context, text string, opts httpapi.VoiceSynthesizeOpts) (io.ReadCloser, error) {
			return s.voiceEngine.Synthesize(ctx, text, TTSOptions{Voice: opts.Voice, Speed: opts.Speed, Format: opts.Format})
		},
		HandleWakeWS:     func(w http.ResponseWriter, r *http.Request) { s.voiceRealtimeEngine.handleWakeWebSocket(w, r) },
		HandleRealtimeWS: func(w http.ResponseWriter, r *http.Request) { s.voiceRealtimeEngine.handleRealtimeWebSocket(w, r) },
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
				run, err := resumeWorkflow(trace.WithID(context.Background(), wfTraceID), cfg, runID, s.state, s.sem, s.childSem)
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
				TetoraExport string   `json:"tetoraExport"`
				Workflow     Workflow `json:"workflow"`
			}
			if err := json.Unmarshal(body, &pkg); err != nil {
				return "", 0, nil, err
			}
			if pkg.TetoraExport == "" {
				return "", 0, []string{"not a valid Tetora export package (missing tetoraExport field)"}, nil
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
					PermissionMode: rc.PermissionMode,
					SoulFile:       rc.SoulFile,
					Description:    rc.Description,
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
		UpdateAgent: func(name, model, permMode, desc, soulFile, soulContent string) error {
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

	// Dashboard.
	mux.HandleFunc("/dashboard/office-bg.webp", handleOfficeBg)
	mux.HandleFunc("/dashboard/sprites/", handleSprite)
	mux.HandleFunc("/dashboard", handleDashboard)

	// Middleware chain: recovery → trace → body size → rate limit → dashboard auth → IP allowlist → API auth → mux
	handler := recoveryMiddleware(trace.Middleware(bodySizeMiddleware(rateLimitMiddleware(cfg, s.apiLimiter,
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
