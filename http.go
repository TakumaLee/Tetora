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
	"strconv"
	"strings"
	"sync"
	"syscall"
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

		// Skip auth for health check, dashboard, and Slack events (uses its own signature verification).
		p := r.URL.Path
		if p == "/healthz" || p == "/dashboard" || strings.HasPrefix(p, "/dashboard/") || p == "/slack/events" || p == "/api/docs" || p == "/api/spec" || strings.HasPrefix(p, "/hooks/") {
			next.ServeHTTP(w, r)
			return
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
		// Always allow healthz for monitoring probes.
		if r.URL.Path == "/healthz" {
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
		// Skip rate limiting for healthz and static dashboard assets.
		p := r.URL.Path
		if p == "/healthz" {
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

func startHTTPServer(addr string, state *dispatchState, cfg *Config, sem chan struct{}, cron *CronEngine, secMon *securityMonitor, mcpHost *MCPHost, voiceEngine *VoiceEngine, slackBot ...*SlackBot) *http.Server {
	startTime := time.Now()
	mux := http.NewServeMux()
	limiter := newLoginLimiter()
	apiLimiter := newAPIRateLimiter(cfg.RateLimit.MaxPerMin)
	allowlist := parseAllowlist(cfg.AllowedIPs)

	// Register Slack events endpoint (uses its own auth via signing secret,
	// registered on mux directly; Slack signature verification is inside the handler).
	if len(slackBot) > 0 && slackBot[0] != nil {
		mux.HandleFunc("/slack/events", slackBot[0].slackEventHandler)
	}

	// --- Health ---
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		checks := deepHealthCheck(cfg, state, cron, startTime)
		b, _ := json.MarshalIndent(checks, "", "  ")
		w.Write(b)
	})

	// --- Circuit Breakers ---
	mux.HandleFunc("/circuits", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var status map[string]any
		if cfg.circuits != nil {
			status = cfg.circuits.status()
		} else {
			status = map[string]any{}
		}
		b, _ := json.MarshalIndent(status, "", "  ")
		w.Write(b)
	})

	mux.HandleFunc("/circuits/", func(w http.ResponseWriter, r *http.Request) {
		// POST /circuits/{provider}/reset
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/circuits/")
		provider := strings.TrimSuffix(path, "/reset")
		if provider == "" || !strings.HasSuffix(path, "/reset") {
			http.Error(w, `{"error":"use POST /circuits/{provider}/reset"}`, http.StatusBadRequest)
			return
		}
		if cfg.circuits == nil {
			http.Error(w, `{"error":"circuit breaker not initialized"}`, http.StatusServiceUnavailable)
			return
		}
		if ok := cfg.circuits.reset(provider); !ok {
			http.Error(w, `{"error":"provider not found"}`, http.StatusNotFound)
			return
		}
		auditLog(cfg.HistoryDB, "circuit.reset", r.RemoteAddr, provider, "")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(fmt.Sprintf(`{"provider":%q,"state":"closed"}`, provider)))
	})

	// --- Offline Queue ---
	mux.HandleFunc("/queue", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
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
				http.Error(w, fmt.Sprintf(`{"error":"item status is %q, must be pending or failed"}`, item.Status), http.StatusConflict)
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
			auditLog(cfg.HistoryDB, "queue.retry", "http", fmt.Sprintf("queueId=%d", id), clientIP(r))

			go func() {
				ctx := withTraceID(context.Background(), newTraceID("queue"))
				result := runSingleTask(ctx, cfg, task, sem, item.RoleName)
				if result.Status == "success" {
					updateQueueStatus(cfg.HistoryDB, id, "completed", "")
				} else {
					incrementQueueRetry(cfg.HistoryDB, id, "failed", result.Error)
				}
				startAt := time.Now().Add(-time.Duration(result.DurationMs) * time.Millisecond)
				recordHistory(cfg.HistoryDB, task.ID, task.Name, task.Source, item.RoleName, task, result,
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
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			auditLog(cfg.HistoryDB, "queue.delete", "http", fmt.Sprintf("queueId=%d", id), clientIP(r))
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

		state.mu.Lock()
		busy := state.active
		state.mu.Unlock()
		if busy {
			http.Error(w, `{"error":"dispatch already running"}`, http.StatusConflict)
			return
		}

		var tasks []Task
		if err := json.NewDecoder(r.Body).Decode(&tasks); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
			return
		}
		for i := range tasks {
			fillDefaults(cfg, &tasks[i])
			tasks[i].Source = "http"
		}

		auditLog(cfg.HistoryDB, "dispatch", "http",
			fmt.Sprintf("%d tasks", len(tasks)), clientIP(r))

		result := dispatch(r.Context(), cfg, tasks, state, sem)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	})

	// --- Cancel ---
	mux.HandleFunc("/cancel", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		state.mu.Lock()
		cancelFn := state.cancel
		state.mu.Unlock()
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
		w.Header().Set("Content-Type", "application/json")

		id := strings.TrimPrefix(r.URL.Path, "/cancel/")
		if id == "" {
			http.Error(w, `{"error":"id required"}`, http.StatusBadRequest)
			return
		}

		// Try dispatch state first.
		state.mu.Lock()
		if ts, ok := state.running[id]; ok && ts.cancelFn != nil {
			ts.cancelFn()
			state.mu.Unlock()
			auditLog(cfg.HistoryDB, "task.cancel", "http",
				fmt.Sprintf("id=%s (dispatch)", id), clientIP(r))
			w.Write([]byte(`{"status":"cancelling"}`))
			return
		}
		state.mu.Unlock()

		// Try cron engine.
		if cron != nil {
			if err := cron.CancelJob(id); err == nil {
				auditLog(cfg.HistoryDB, "job.cancel", "http",
					fmt.Sprintf("id=%s (cron)", id), clientIP(r))
				w.Write([]byte(`{"status":"cancelling"}`))
				return
			}
		}

		http.Error(w, `{"error":"task not found or not running"}`, http.StatusNotFound)
	})

	// --- Cron: list + create ---
	mux.HandleFunc("/cron", func(w http.ResponseWriter, r *http.Request) {
		if cron == nil {
			http.Error(w, `{"error":"cron not available"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodGet:
			json.NewEncoder(w).Encode(cron.ListJobs())

		case http.MethodPost:
			var jc CronJobConfig
			if err := json.NewDecoder(r.Body).Decode(&jc); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}
			if jc.ID == "" || jc.Schedule == "" {
				http.Error(w, `{"error":"id and schedule are required"}`, http.StatusBadRequest)
				return
			}
			if err := cron.AddJob(jc); err != nil {
				code := http.StatusBadRequest
				if strings.Contains(err.Error(), "already exists") {
					code = http.StatusConflict
				}
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), code)
				return
			}
			auditLog(cfg.HistoryDB, "job.create", "http",
				fmt.Sprintf("id=%s schedule=%s", jc.ID, jc.Schedule), clientIP(r))
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"status":"created"}`))

		default:
			http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
		}
	})

	// --- Cron: per-job actions ---
	mux.HandleFunc("/cron/", func(w http.ResponseWriter, r *http.Request) {
		if cron == nil {
			http.Error(w, `{"error":"cron not available"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		// Parse /cron/<id>/<action>
		path := strings.TrimPrefix(r.URL.Path, "/cron/")
		parts := strings.SplitN(path, "/", 2)
		id := parts[0]
		action := ""
		if len(parts) > 1 {
			action = parts[1]
		}

		switch {
		// GET /cron/<id> — get full job config
		case action == "" && r.Method == http.MethodGet:
			jc := cron.GetJobConfig(id)
			if jc == nil {
				http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(jc)

		// PUT /cron/<id> — update job
		case action == "" && r.Method == http.MethodPut:
			var jc CronJobConfig
			if err := json.NewDecoder(r.Body).Decode(&jc); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}
			if jc.Schedule == "" {
				http.Error(w, `{"error":"schedule is required"}`, http.StatusBadRequest)
				return
			}
			if err := cron.UpdateJob(id, jc); err != nil {
				code := http.StatusBadRequest
				if strings.Contains(err.Error(), "not found") {
					code = http.StatusNotFound
				}
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), code)
				return
			}
			auditLog(cfg.HistoryDB, "job.update", "http",
				fmt.Sprintf("id=%s", id), clientIP(r))
			w.Write([]byte(`{"status":"updated"}`))

		// DELETE /cron/<id> — remove job
		case action == "" && r.Method == http.MethodDelete:
			if err := cron.RemoveJob(id); err != nil {
				code := http.StatusBadRequest
				if strings.Contains(err.Error(), "not found") {
					code = http.StatusNotFound
				}
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), code)
				return
			}
			auditLog(cfg.HistoryDB, "job.delete", "http",
				fmt.Sprintf("id=%s", id), clientIP(r))
			w.Write([]byte(`{"status":"removed"}`))

		// POST /cron/<id>/toggle
		case action == "toggle" && r.Method == http.MethodPost:
			var body struct {
				Enabled bool `json:"enabled"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if err := cron.ToggleJob(id, body.Enabled); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
				return
			}
			auditLog(cfg.HistoryDB, "job.toggle", "http",
				fmt.Sprintf("id=%s enabled=%v", id, body.Enabled), clientIP(r))
			w.Write([]byte(fmt.Sprintf(`{"status":"ok","enabled":%v}`, body.Enabled)))

		// POST /cron/<id>/approve
		case action == "approve" && r.Method == http.MethodPost:
			if err := cron.ApproveJob(id); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}
			auditLog(cfg.HistoryDB, "job.approve", "http",
				fmt.Sprintf("id=%s", id), clientIP(r))
			w.Write([]byte(`{"status":"approved"}`))

		// POST /cron/<id>/reject
		case action == "reject" && r.Method == http.MethodPost:
			if err := cron.RejectJob(id); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}
			auditLog(cfg.HistoryDB, "job.reject", "http",
				fmt.Sprintf("id=%s", id), clientIP(r))
			w.Write([]byte(`{"status":"rejected"}`))

		// POST /cron/<id>/run
		case action == "run" && r.Method == http.MethodPost:
			if err := cron.RunJobByID(r.Context(), id); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}
			auditLog(cfg.HistoryDB, "job.trigger", "http",
				fmt.Sprintf("id=%s", id), clientIP(r))
			w.Write([]byte(`{"status":"triggered"}`))

		default:
			http.Error(w, `{"error":"unknown action"}`, http.StatusBadRequest)
		}
	})

	// --- History ---
	mux.HandleFunc("/history", func(w http.ResponseWriter, r *http.Request) {
		if cfg.HistoryDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		q := HistoryQuery{
			JobID:  r.URL.Query().Get("job_id"),
			Status: r.URL.Query().Get("status"),
			From:   r.URL.Query().Get("from"),
			To:     r.URL.Query().Get("to"),
			Limit:  20,
		}
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 {
				q.Limit = n
			}
		}
		if p := r.URL.Query().Get("page"); p != "" {
			if n, err := strconv.Atoi(p); err == nil && n > 1 {
				q.Offset = (n - 1) * q.Limit
			}
		}
		if o := r.URL.Query().Get("offset"); o != "" {
			if n, err := strconv.Atoi(o); err == nil && n >= 0 {
				q.Offset = n
			}
		}

		runs, total, err := queryHistoryFiltered(cfg.HistoryDB, q)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if runs == nil {
			runs = []JobRun{}
		}

		page := (q.Offset / q.Limit) + 1
		json.NewEncoder(w).Encode(map[string]any{
			"runs":  runs,
			"total": total,
			"page":  page,
			"limit": q.Limit,
		})
	})

	mux.HandleFunc("/history/", func(w http.ResponseWriter, r *http.Request) {
		if cfg.HistoryDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		idStr := strings.TrimPrefix(r.URL.Path, "/history/")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
			return
		}

		run, err := queryHistoryByID(cfg.HistoryDB, id)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if run == nil {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(run)
	})

	// --- Cost Stats ---
	mux.HandleFunc("/stats/cost", func(w http.ResponseWriter, r *http.Request) {
		if cfg.HistoryDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		stats, err := queryCostStats(cfg.HistoryDB)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		result := map[string]any{
			"today": stats.Today,
			"week":  stats.Week,
			"month": stats.Month,
		}

		// Include cost alert config if limits are set.
		if cfg.CostAlert.DailyLimit > 0 || cfg.CostAlert.WeeklyLimit > 0 {
			result["dailyLimit"] = cfg.CostAlert.DailyLimit
			result["weeklyLimit"] = cfg.CostAlert.WeeklyLimit
			result["alertAction"] = cfg.CostAlert.Action
			result["dailyExceeded"] = cfg.CostAlert.DailyLimit > 0 && stats.Today >= cfg.CostAlert.DailyLimit
			result["weeklyExceeded"] = cfg.CostAlert.WeeklyLimit > 0 && stats.Week >= cfg.CostAlert.WeeklyLimit
		}

		json.NewEncoder(w).Encode(result)
	})

	// --- Trend Stats ---
	mux.HandleFunc("/stats/trend", func(w http.ResponseWriter, r *http.Request) {
		if cfg.HistoryDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		days := 7
		if d := r.URL.Query().Get("days"); d != "" {
			if n, err := strconv.Atoi(d); err == nil && n > 0 && n <= 90 {
				days = n
			}
		}

		stats, err := queryDailyStats(cfg.HistoryDB, days)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if stats == nil {
			stats = []DayStat{}
		}
		json.NewEncoder(w).Encode(stats)
	})

	// --- Metrics Stats ---
	mux.HandleFunc("/stats/metrics", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		if cfg.HistoryDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		days := 30
		if d := r.URL.Query().Get("days"); d != "" {
			if n, err := strconv.Atoi(d); err == nil && n > 0 && n <= 365 {
				days = n
			}
		}

		summary, err := queryMetrics(cfg.HistoryDB, days)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		daily, err := queryDailyMetrics(cfg.HistoryDB, days)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if daily == nil {
			daily = []DailyMetrics{}
		}

		byModel, err := queryProviderMetrics(cfg.HistoryDB, days)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if byModel == nil {
			byModel = []ProviderMetrics{}
		}

		json.NewEncoder(w).Encode(map[string]any{
			"days":    days,
			"summary": summary,
			"daily":   daily,
			"byModel": byModel,
		})
	})

	// --- Routing Stats ---
	mux.HandleFunc("/stats/routing", func(w http.ResponseWriter, r *http.Request) {
		if cfg.HistoryDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		limit := 50
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 200 {
				limit = n
			}
		}

		history, byRole, err := queryRoutingStats(cfg.HistoryDB, limit)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if history == nil {
			history = []RoutingHistoryEntry{}
		}
		if byRole == nil {
			byRole = map[string]*RoleRoutingStats{}
		}

		json.NewEncoder(w).Encode(map[string]any{
			"history": history,
			"byRole":  byRole,
			"total":   len(history),
		})
	})

	// --- SLA Stats ---
	mux.HandleFunc("/stats/sla", func(w http.ResponseWriter, r *http.Request) {
		if cfg.HistoryDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodGet:
			statuses, err := querySLAStatusAll(cfg)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if statuses == nil {
				statuses = []SLAStatus{}
			}

			// Also fetch recent check history.
			role := r.URL.Query().Get("role")
			limit := 24
			if l := r.URL.Query().Get("limit"); l != "" {
				if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
					limit = n
				}
			}
			history, _ := querySLAHistory(cfg.HistoryDB, role, limit)
			if history == nil {
				history = []SLACheckResult{}
			}

			json.NewEncoder(w).Encode(map[string]any{
				"statuses": statuses,
				"history":  history,
				"config":   cfg.SLA,
			})
		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	})

	// --- Roles: archetypes ---
	mux.HandleFunc("/roles/archetypes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		type archInfo struct {
			Name           string `json:"name"`
			Description    string `json:"description"`
			Model          string `json:"model"`
			PermissionMode string `json:"permissionMode"`
			SoulTemplate   string `json:"soulTemplate"`
		}
		var archs []archInfo
		for _, a := range builtinArchetypes {
			archs = append(archs, archInfo{
				Name:           a.Name,
				Description:    a.Description,
				Model:          a.Model,
				PermissionMode: a.PermissionMode,
				SoulTemplate:   a.SoulTemplate,
			})
		}
		json.NewEncoder(w).Encode(archs)
	})

	// --- Roles: list + create ---
	mux.HandleFunc("/roles", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodGet:
			type roleInfo struct {
				Name           string `json:"name"`
				Model          string `json:"model"`
				PermissionMode string `json:"permissionMode,omitempty"`
				SoulFile       string `json:"soulFile"`
				Description    string `json:"description"`
				SoulPreview    string `json:"soulPreview,omitempty"`
			}

			var roles []roleInfo
			for name, rc := range cfg.Roles {
				ri := roleInfo{
					Name:           name,
					Model:          rc.Model,
					PermissionMode: rc.PermissionMode,
					SoulFile:       rc.SoulFile,
					Description:    rc.Description,
				}
				// Load soul file preview.
				if content, err := loadRolePrompt(cfg, name); err == nil && content != "" {
					if len(content) > 500 {
						ri.SoulPreview = content[:500] + "..."
					} else {
						ri.SoulPreview = content
					}
				}
				roles = append(roles, ri)
			}
			if roles == nil {
				roles = []roleInfo{}
			}
			json.NewEncoder(w).Encode(roles)

		case http.MethodPost:
			var body struct {
				Name           string `json:"name"`
				Model          string `json:"model"`
				PermissionMode string `json:"permissionMode"`
				Description    string `json:"description"`
				SoulFile       string `json:"soulFile"`
				SoulContent    string `json:"soulContent"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}
			if body.Name == "" {
				http.Error(w, `{"error":"name is required"}`, http.StatusBadRequest)
				return
			}
			if _, exists := cfg.Roles[body.Name]; exists {
				http.Error(w, `{"error":"role already exists"}`, http.StatusConflict)
				return
			}

			// Default soul file name if not specified.
			if body.SoulFile == "" {
				body.SoulFile = fmt.Sprintf("SOUL-%s.md", body.Name)
			}

			// Write soul content to file.
			if body.SoulContent != "" {
				if err := writeSoulFile(cfg, body.SoulFile, body.SoulContent); err != nil {
					http.Error(w, fmt.Sprintf(`{"error":"write soul file: %v"}`, err), http.StatusInternalServerError)
					return
				}
			}

			rc := RoleConfig{
				SoulFile:       body.SoulFile,
				Model:          body.Model,
				Description:    body.Description,
				PermissionMode: body.PermissionMode,
			}

			configPath := findConfigPath()
			if err := updateConfigRoles(configPath, body.Name, &rc); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"save config: %v"}`, err), http.StatusInternalServerError)
				return
			}

			// Hot-reload into memory.
			if cfg.Roles == nil {
				cfg.Roles = make(map[string]RoleConfig)
			}
			cfg.Roles[body.Name] = rc

			auditLog(cfg.HistoryDB, "role.create", "http",
				fmt.Sprintf("name=%s", body.Name), clientIP(r))
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"status":"created"}`))

		default:
			http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
		}
	})

	// --- Roles: per-role actions ---
	mux.HandleFunc("/roles/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Parse /roles/<name> - skip the archetypes path.
		path := strings.TrimPrefix(r.URL.Path, "/roles/")
		if path == "" || path == "archetypes" {
			return // handled by other handlers
		}
		name := path

		switch r.Method {
		case http.MethodGet:
			rc, ok := cfg.Roles[name]
			if !ok {
				http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
				return
			}
			result := map[string]any{
				"name":           name,
				"model":          rc.Model,
				"permissionMode": rc.PermissionMode,
				"soulFile":       rc.SoulFile,
				"description":    rc.Description,
			}
			// Load full soul content (not just preview).
			if content, err := loadRolePrompt(cfg, name); err == nil {
				result["soulContent"] = content
			}
			json.NewEncoder(w).Encode(result)

		case http.MethodPut:
			rc, ok := cfg.Roles[name]
			if !ok {
				http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
				return
			}
			var body struct {
				Model          string `json:"model"`
				PermissionMode string `json:"permissionMode"`
				Description    string `json:"description"`
				SoulFile       string `json:"soulFile"`
				SoulContent    string `json:"soulContent"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}

			// Update fields.
			if body.Model != "" {
				rc.Model = body.Model
			}
			if body.PermissionMode != "" {
				rc.PermissionMode = body.PermissionMode
			}
			if body.Description != "" {
				rc.Description = body.Description
			}
			if body.SoulFile != "" {
				rc.SoulFile = body.SoulFile
			}
			if body.SoulContent != "" {
				soulFile := rc.SoulFile
				if soulFile == "" {
					soulFile = fmt.Sprintf("SOUL-%s.md", name)
					rc.SoulFile = soulFile
				}
				if err := writeSoulFile(cfg, soulFile, body.SoulContent); err != nil {
					http.Error(w, fmt.Sprintf(`{"error":"write soul: %v"}`, err), http.StatusInternalServerError)
					return
				}
			}

			configPath := findConfigPath()
			if err := updateConfigRoles(configPath, name, &rc); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"save: %v"}`, err), http.StatusInternalServerError)
				return
			}
			cfg.Roles[name] = rc
			auditLog(cfg.HistoryDB, "role.update", "http",
				fmt.Sprintf("name=%s", name), clientIP(r))
			w.Write([]byte(`{"status":"updated"}`))

		case http.MethodDelete:
			if _, ok := cfg.Roles[name]; !ok {
				http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
				return
			}
			// Check if any job uses this role.
			if cron != nil {
				for _, j := range cron.ListJobs() {
					if j.Role == name {
						http.Error(w, fmt.Sprintf(`{"error":"role in use by job %q"}`, j.ID), http.StatusConflict)
						return
					}
				}
			}
			configPath := findConfigPath()
			if err := updateConfigRoles(configPath, name, nil); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"save: %v"}`, err), http.StatusInternalServerError)
				return
			}
			delete(cfg.Roles, name)
			auditLog(cfg.HistoryDB, "role.delete", "http",
				fmt.Sprintf("name=%s", name), clientIP(r))
			w.Write([]byte(`{"status":"deleted"}`))

		default:
			http.Error(w, "GET, PUT or DELETE only", http.StatusMethodNotAllowed)
		}
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
		}

		var tasks []runningTask

		// From dispatch state.
		state.mu.Lock()
		for _, ts := range state.running {
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
			})
		}
		state.mu.Unlock()

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

	// --- Tasks (Dashboard DB) ---
	mux.HandleFunc("/tasks", func(w http.ResponseWriter, r *http.Request) {
		if cfg.DashboardDB == "" {
			http.Error(w, `{"error":"dashboard DB not configured"}`, http.StatusServiceUnavailable)
			return
		}

		switch r.Method {
		case http.MethodGet:
			status := r.URL.Query().Get("status")
			if status != "" {
				tasks, err := getTasksByStatus(cfg.DashboardDB, status)
				if err != nil {
					http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(tasks)
			} else {
				stats, err := getTaskStats(cfg.DashboardDB)
				if err != nil {
					http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
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
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}
			if err := updateTaskStatus(cfg.DashboardDB, body.ID, body.Status, body.Error); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
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
		name := strings.TrimPrefix(r.URL.Path, "/outputs/")
		// Strict filename validation: only allow alphanumeric, dash, underscore, dot.
		if name == "" || !isValidOutputFilename(name) {
			http.Error(w, `{"error":"invalid filename"}`, http.StatusBadRequest)
			return
		}
		outputDir := filepath.Join(cfg.baseDir, "outputs")
		filePath := filepath.Join(outputDir, name)
		// Verify resolved path is still within outputs dir (prevent symlink escape).
		absPath, err := filepath.Abs(filePath)
		if err != nil || !strings.HasPrefix(absPath, filepath.Join(cfg.baseDir, "outputs")) {
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

		// Parse multipart form (max 50MB).
		if err := r.ParseMultipartForm(50 << 20); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"parse form: %s"}`, err), http.StatusBadRequest)
			return
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"no file: %s"}`, err), http.StatusBadRequest)
			return
		}
		defer file.Close()

		uploadDir := initUploadDir(cfg.baseDir)
		uploaded, err := saveUpload(uploadDir, header.Filename, file, header.Size, "http")
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}

		auditLog(cfg.HistoryDB, "file.upload", "http", uploaded.Name, clientIP(r))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(uploaded)
	})

	// --- Prompt Library ---
	mux.HandleFunc("/prompts", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case "GET":
			prompts, err := listPrompts(cfg)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
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
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
				return
			}
			auditLog(cfg.HistoryDB, "prompt.create", "http", body.Name, clientIP(r))
			json.NewEncoder(w).Encode(map[string]string{"status": "ok", "name": body.Name})

		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/prompts/", func(w http.ResponseWriter, r *http.Request) {
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
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"name": name, "content": content})

		case "DELETE":
			if err := deletePrompt(cfg, name); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusNotFound)
				return
			}
			auditLog(cfg.HistoryDB, "prompt.delete", "http", name, clientIP(r))
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	})

	// --- MCP Configs ---
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case "GET":
			configs := listMCPConfigs(cfg)
			if configs == nil {
				configs = []MCPConfigInfo{}
			}
			json.NewEncoder(w).Encode(configs)

		case "POST":
			var body struct {
				Name   string          `json:"name"`
				Config json.RawMessage `json:"config"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
				return
			}
			if body.Name == "" || len(body.Config) == 0 {
				http.Error(w, `{"error":"name and config are required"}`, http.StatusBadRequest)
				return
			}
			configPath := findConfigPath()
			if err := setMCPConfig(cfg, configPath, body.Name, body.Config); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}
			auditLog(cfg.HistoryDB, "mcp.save", "http", body.Name, clientIP(r))
			json.NewEncoder(w).Encode(map[string]string{"status": "ok", "name": body.Name})

		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/mcp/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		path := strings.TrimPrefix(r.URL.Path, "/mcp/")
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

		switch {
		case action == "" && r.Method == "GET":
			raw, err := getMCPConfig(cfg, name)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"name": name, "config": json.RawMessage(raw)})

		case action == "" && r.Method == "DELETE":
			configPath := findConfigPath()
			if err := deleteMCPConfig(cfg, configPath, name); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusNotFound)
				return
			}
			auditLog(cfg.HistoryDB, "mcp.delete", "http", name, clientIP(r))
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		case action == "test" && r.Method == "POST":
			raw, err := getMCPConfig(cfg, name)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusNotFound)
				return
			}
			ok, output := testMCPConfig(raw)
			json.NewEncoder(w).Encode(map[string]any{"ok": ok, "output": output})

		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	})

	// --- Agent Memory ---
	mux.HandleFunc("/memory", func(w http.ResponseWriter, r *http.Request) {
		if cfg.HistoryDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case "GET":
			role := r.URL.Query().Get("role")
			entries, err := listMemory(cfg.HistoryDB, role)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
				return
			}
			if entries == nil {
				entries = []MemoryEntry{}
			}
			json.NewEncoder(w).Encode(entries)

		case "POST":
			var body struct {
				Role  string `json:"role"`
				Key   string `json:"key"`
				Value string `json:"value"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
				return
			}
			if body.Role == "" || body.Key == "" {
				http.Error(w, `{"error":"role and key are required"}`, http.StatusBadRequest)
				return
			}
			if err := setMemory(cfg.HistoryDB, body.Role, body.Key, body.Value); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
				return
			}
			auditLog(cfg.HistoryDB, "memory.set", "http",
				fmt.Sprintf("role=%s key=%s", body.Role, body.Key), clientIP(r))
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/memory/", func(w http.ResponseWriter, r *http.Request) {
		if cfg.HistoryDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		// Parse /memory/<role>/<key>
		path := strings.TrimPrefix(r.URL.Path, "/memory/")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			http.Error(w, `{"error":"path must be /memory/{role}/{key}"}`, http.StatusBadRequest)
			return
		}
		role := parts[0]
		key := parts[1]

		switch r.Method {
		case "GET":
			val, err := getMemory(cfg.HistoryDB, role, key)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{
				"role": role, "key": key, "value": val,
			})

		case "DELETE":
			if err := deleteMemory(cfg.HistoryDB, role, key); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
				return
			}
			auditLog(cfg.HistoryDB, "memory.delete", "http",
				fmt.Sprintf("role=%s key=%s", role, key), clientIP(r))
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	})

	// --- Sessions ---
	mux.HandleFunc("/sessions", func(w http.ResponseWriter, r *http.Request) {
		if cfg.HistoryDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodGet:
			q := SessionQuery{
				Role:   r.URL.Query().Get("role"),
				Status: r.URL.Query().Get("status"),
				Source: r.URL.Query().Get("source"),
				Limit:  20,
			}
			if l := r.URL.Query().Get("limit"); l != "" {
				if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
					q.Limit = n
				}
			}
			if p := r.URL.Query().Get("page"); p != "" {
				if n, err := strconv.Atoi(p); err == nil && n > 1 {
					q.Offset = (n - 1) * q.Limit
				}
			}

			sessions, total, err := querySessions(cfg.HistoryDB, q)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if sessions == nil {
				sessions = []Session{}
			}
			page := (q.Offset / q.Limit) + 1
			json.NewEncoder(w).Encode(map[string]any{
				"sessions": sessions,
				"total":    total,
				"page":     page,
				"limit":    q.Limit,
			})

		case http.MethodPost:
			var body struct {
				Role  string `json:"role"`
				Title string `json:"title"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
				return
			}
			if body.Role == "" {
				http.Error(w, `{"error":"role is required"}`, http.StatusBadRequest)
				return
			}
			if _, ok := cfg.Roles[body.Role]; !ok {
				http.Error(w, `{"error":"role not found"}`, http.StatusBadRequest)
				return
			}
			now := time.Now().Format(time.RFC3339)
			sess := Session{
				ID:        newUUID(),
				Role:      body.Role,
				Source:    "chat",
				Status:    "active",
				Title:     body.Title,
				CreatedAt: now,
				UpdatedAt: now,
			}
			if sess.Title == "" {
				sess.Title = "New chat with " + body.Role
			}
			if err := createSession(cfg.HistoryDB, sess); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			auditLog(cfg.HistoryDB, "session.create", "http",
				fmt.Sprintf("session=%s role=%s", sess.ID, sess.Role), clientIP(r))
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(sess)

		default:
			http.Error(w, `{"error":"GET or POST only"}`, http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/sessions/", func(w http.ResponseWriter, r *http.Request) {
		if cfg.HistoryDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		path := strings.TrimPrefix(r.URL.Path, "/sessions/")
		if path == "" {
			http.Error(w, `{"error":"session id required"}`, http.StatusBadRequest)
			return
		}

		parts := strings.SplitN(path, "/", 2)
		sessionID := parts[0]
		action := ""
		if len(parts) > 1 {
			action = parts[1]
		}

		switch {
		// GET /sessions/{id}/stream — SSE stream for session events.
		case action == "stream" && r.Method == http.MethodGet:
			if state.broker == nil {
				http.Error(w, `{"error":"streaming not available"}`, http.StatusServiceUnavailable)
				return
			}
			serveSSE(w, r, state.broker, sessionID)
			return

		// GET /sessions/{id} — get session with messages.
		case action == "" && r.Method == http.MethodGet:
			detail, err := querySessionDetail(cfg.HistoryDB, sessionID)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if detail == nil {
				http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(detail)

		// DELETE /sessions/{id} — archive session.
		case action == "" && r.Method == http.MethodDelete:
			if err := updateSessionStatus(cfg.HistoryDB, sessionID, "archived"); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			auditLog(cfg.HistoryDB, "session.archive", "http",
				fmt.Sprintf("session=%s", sessionID), clientIP(r))
			w.Write([]byte(`{"status":"archived"}`))

		// POST /sessions/{id}/message — continue a session.
		case action == "message" && r.Method == http.MethodPost:
			var body struct {
				Prompt string `json:"prompt"`
				Async  bool   `json:"async"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Prompt == "" {
				http.Error(w, `{"error":"prompt is required"}`, http.StatusBadRequest)
				return
			}

			sess, err := querySessionByID(cfg.HistoryDB, sessionID)
			if err != nil || sess == nil {
				http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
				return
			}

			// Pre-record user message immediately.
			now := time.Now().Format(time.RFC3339)
			addSessionMessage(cfg.HistoryDB, SessionMessage{
				SessionID: sessionID,
				Role:      "user",
				Content:   truncateStr(body.Prompt, 5000),
				CreatedAt: now,
			})
			updateSessionStats(cfg.HistoryDB, sessionID, 0, 0, 0, 1)

			// Update session title on first message.
			title := body.Prompt
			if len(title) > 100 {
				title = title[:100]
			}
			updateSessionTitle(cfg.HistoryDB, sessionID, title)

			// Re-activate session if it was completed.
			if sess.Status == "completed" {
				updateSessionStatus(cfg.HistoryDB, sessionID, "active")
			}

			task := Task{
				Prompt:    body.Prompt,
				Role:      sess.Role,
				SessionID: sessionID,
				Source:    "chat",
			}
			fillDefaults(cfg, &task)
			task.SessionID = sessionID // Override fillDefaults' new UUID.

			if body.Async {
				// Async mode: return task ID immediately, stream via SSE.
				taskID := task.ID
				traceID := traceIDFromContext(r.Context())

				go func() {
					asyncCtx := withTraceID(context.Background(), traceID)
					result := runTask(asyncCtx, cfg, task, state)

					// Record assistant message to session.
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

				auditLog(cfg.HistoryDB, "session.message.async", "http",
					fmt.Sprintf("session=%s role=%s task=%s", sessionID, sess.Role, taskID), clientIP(r))
				w.WriteHeader(http.StatusAccepted)
				json.NewEncoder(w).Encode(map[string]any{
					"taskId":    taskID,
					"sessionId": sessionID,
					"status":    "running",
				})
				return
			}

			// Sync mode (existing behavior for API consumers).
			result := runSingleTask(r.Context(), cfg, task, sem, sess.Role)
			taskStart := time.Now().Add(-time.Duration(result.DurationMs) * time.Millisecond)
			recordHistory(cfg.HistoryDB, task.ID, task.Name, task.Source, sess.Role, task, result,
				taskStart.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)

			// Record assistant message (user message already pre-recorded above).
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

			auditLog(cfg.HistoryDB, "session.message", "http",
				fmt.Sprintf("session=%s role=%s", sessionID, sess.Role), clientIP(r))
			json.NewEncoder(w).Encode(result)

		// POST /sessions/{id}/compact — trigger context compaction.
		case action == "compact" && r.Method == http.MethodPost:
			go func() {
				compactCtx, compactCancel := context.WithTimeout(context.Background(), 2*time.Minute)
				defer compactCancel()
				if err := compactSession(compactCtx, cfg, cfg.HistoryDB, sessionID, sem); err != nil {
					logError("compact session error", "session", sessionID, "error", err)
				}
			}()
			auditLog(cfg.HistoryDB, "session.compact", "http",
				fmt.Sprintf("session=%s", sessionID), clientIP(r))
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{"status": "compacting"})

		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	})

	// --- Skills ---
	mux.HandleFunc("/skills", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		skills := listSkills(cfg)
		json.NewEncoder(w).Encode(skills)
	})

	mux.HandleFunc("/skills/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Parse /skills/<name>/<action>
		path := strings.TrimPrefix(r.URL.Path, "/skills/")
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

		skill := getSkill(cfg, name)
		if skill == nil {
			http.Error(w, fmt.Sprintf(`{"error":"skill %q not found"}`, name), http.StatusNotFound)
			return
		}

		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}

		switch action {
		case "run":
			var body struct {
				Vars map[string]string `json:"vars"`
			}
			json.NewDecoder(r.Body).Decode(&body)

			auditLog(cfg.HistoryDB, "skill.run", "http",
				fmt.Sprintf("name=%s", name), clientIP(r))

			result, err := executeSkill(r.Context(), *skill, body.Vars)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(result)

		case "test":
			auditLog(cfg.HistoryDB, "skill.test", "http",
				fmt.Sprintf("name=%s", name), clientIP(r))

			result, err := testSkill(r.Context(), *skill)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(result)

		default:
			http.Error(w, `{"error":"unknown action, use run or test"}`, http.StatusBadRequest)
		}
	})

	// --- Workflows ---

	mux.HandleFunc("/workflows", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			workflows, err := listWorkflows(cfg)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if workflows == nil {
				workflows = []*Workflow{}
			}
			json.NewEncoder(w).Encode(workflows)

		case http.MethodPost:
			var wf Workflow
			if err := json.NewDecoder(r.Body).Decode(&wf); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"invalid JSON: %v"}`, err), http.StatusBadRequest)
				return
			}
			errs := validateWorkflow(&wf)
			if len(errs) > 0 {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]any{"errors": errs})
				return
			}
			if err := saveWorkflow(cfg, &wf); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			auditLog(cfg.HistoryDB, "workflow.create", "http",
				fmt.Sprintf("name=%s steps=%d", wf.Name, len(wf.Steps)), clientIP(r))
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{"status": "created", "name": wf.Name})

		default:
			http.Error(w, `{"error":"GET or POST"}`, http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/workflows/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		name := strings.TrimPrefix(r.URL.Path, "/workflows/")
		if name == "" {
			http.Error(w, `{"error":"name required"}`, http.StatusBadRequest)
			return
		}

		// Strip sub-paths (e.g. /workflows/name/validate).
		parts := strings.SplitN(name, "/", 2)
		name = parts[0]
		action := ""
		if len(parts) > 1 {
			action = parts[1]
		}

		switch {
		case action == "validate" && r.Method == http.MethodPost:
			wf, err := loadWorkflowByName(cfg, name)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
				return
			}
			errs := validateWorkflow(wf)
			valid := len(errs) == 0
			resp := map[string]any{"valid": valid, "name": wf.Name}
			if !valid {
				resp["errors"] = errs
			} else {
				resp["executionOrder"] = topologicalSort(wf.Steps)
			}
			json.NewEncoder(w).Encode(resp)

		case action == "" && r.Method == http.MethodGet:
			wf, err := loadWorkflowByName(cfg, name)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(wf)

		case action == "" && r.Method == http.MethodDelete:
			if err := deleteWorkflow(cfg, name); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
				return
			}
			auditLog(cfg.HistoryDB, "workflow.delete", "http",
				fmt.Sprintf("name=%s", name), clientIP(r))
			json.NewEncoder(w).Encode(map[string]string{"status": "deleted", "name": name})

		case action == "run" && r.Method == http.MethodPost:
			wf, err := loadWorkflowByName(cfg, name)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
				return
			}
			if errs := validateWorkflow(wf); len(errs) > 0 {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]any{"errors": errs})
				return
			}
			var body struct {
				Variables map[string]string `json:"variables"`
			}
			json.NewDecoder(r.Body).Decode(&body)

			auditLog(cfg.HistoryDB, "workflow.run", "http",
				fmt.Sprintf("name=%s", name), clientIP(r))

			// Run asynchronously.
			wfTraceID := traceIDFromContext(r.Context())
			go executeWorkflow(withTraceID(context.Background(), wfTraceID), cfg, wf, body.Variables, state, sem)

			// Return immediately with run acknowledgment.
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{
				"status":   "accepted",
				"workflow": name,
			})

		case action == "runs" && r.Method == http.MethodGet:
			runs, err := queryWorkflowRuns(cfg.HistoryDB, 20, name)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if runs == nil {
				runs = []WorkflowRun{}
			}
			json.NewEncoder(w).Encode(runs)

		default:
			http.Error(w, `{"error":"GET, DELETE, or POST .../validate|run"}`, http.StatusMethodNotAllowed)
		}
	})

	// --- Workflow Runs ---

	mux.HandleFunc("/workflow-runs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		name := r.URL.Query().Get("workflow")
		runs, err := queryWorkflowRuns(cfg.HistoryDB, 20, name)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if runs == nil {
			runs = []WorkflowRun{}
		}
		json.NewEncoder(w).Encode(runs)
	})

	mux.HandleFunc("/workflow-runs/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		runID := strings.TrimPrefix(r.URL.Path, "/workflow-runs/")
		if runID == "" {
			http.Error(w, `{"error":"run ID required"}`, http.StatusBadRequest)
			return
		}
		run, err := queryWorkflowRunByID(cfg.HistoryDB, runID)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
			return
		}
		// Enrich with handoffs and messages.
		handoffs, _ := queryHandoffs(cfg.HistoryDB, run.ID)
		messages, _ := queryAgentMessages(cfg.HistoryDB, run.ID, "", 100)
		if handoffs == nil {
			handoffs = []Handoff{}
		}
		if messages == nil {
			messages = []AgentMessage{}
		}
		result := map[string]any{
			"run":      run,
			"handoffs": handoffs,
			"messages": messages,
		}
		json.NewEncoder(w).Encode(result)
	})

	// --- Agent Messages ---

	mux.HandleFunc("/agent-messages", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			workflowRun := r.URL.Query().Get("workflowRun")
			role := r.URL.Query().Get("role")
			limit := 50
			if l := r.URL.Query().Get("limit"); l != "" {
				if n, err := strconv.Atoi(l); err == nil && n > 0 {
					limit = n
				}
			}
			msgs, err := queryAgentMessages(cfg.HistoryDB, workflowRun, role, limit)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if msgs == nil {
				msgs = []AgentMessage{}
			}
			json.NewEncoder(w).Encode(msgs)

		case http.MethodPost:
			var msg AgentMessage
			if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}
			if msg.FromRole == "" || msg.ToRole == "" || msg.Content == "" {
				http.Error(w, `{"error":"fromRole, toRole, and content are required"}`, http.StatusBadRequest)
				return
			}
			if msg.Type == "" {
				msg.Type = "note"
			}
			if err := sendAgentMessage(cfg.HistoryDB, msg); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			auditLog(cfg.HistoryDB, "agent.message", "http",
				fmt.Sprintf("%s→%s type=%s", msg.FromRole, msg.ToRole, msg.Type), clientIP(r))
			json.NewEncoder(w).Encode(map[string]string{"status": "sent", "id": msg.ID})

		default:
			http.Error(w, `{"error":"GET or POST only"}`, http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/handoffs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		workflowRun := r.URL.Query().Get("workflowRun")
		handoffs, err := queryHandoffs(cfg.HistoryDB, workflowRun)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if handoffs == nil {
			handoffs = []Handoff{}
		}
		json.NewEncoder(w).Encode(handoffs)
	})

	// --- Knowledge Search ---

	mux.HandleFunc("/knowledge/search", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query().Get("q")
		if q == "" {
			json.NewEncoder(w).Encode([]SearchResult{})
			return
		}
		limit := 10
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 {
				limit = n
			}
		}
		idx, err := buildKnowledgeIndex(cfg.KnowledgeDir)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		results := idx.search(q, limit)
		if results == nil {
			results = []SearchResult{}
		}
		json.NewEncoder(w).Encode(results)
	})

	// --- Reflections ---

	mux.HandleFunc("/reflections", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		if cfg.HistoryDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		role := r.URL.Query().Get("role")
		limit := 20
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 {
				limit = n
			}
		}
		refs, err := queryReflections(cfg.HistoryDB, role, limit)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if refs == nil {
			refs = []ReflectionResult{}
		}
		json.NewEncoder(w).Encode(refs)
	})

	// --- Tool Engine ---

	mux.HandleFunc("/api/tools", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if cfg.toolRegistry == nil {
			json.NewEncoder(w).Encode([]any{})
			return
		}
		tools := cfg.toolRegistry.List()
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
		json.NewEncoder(w).Encode(result)
	})

	// --- MCP Host ---

	mux.HandleFunc("/api/mcp/servers", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if mcpHost == nil {
			json.NewEncoder(w).Encode([]any{})
			return
		}
		statuses := mcpHost.ServerStatus()
		json.NewEncoder(w).Encode(statuses)
	})

	mux.HandleFunc("/api/mcp/servers/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		if mcpHost == nil {
			http.Error(w, `{"error":"MCP host not enabled"}`, http.StatusBadRequest)
			return
		}
		// Extract server name from path
		path := strings.TrimPrefix(r.URL.Path, "/api/mcp/servers/")
		parts := strings.Split(path, "/")
		if len(parts) != 2 || parts[1] != "restart" {
			http.Error(w, `{"error":"invalid path, use /api/mcp/servers/{name}/restart"}`, http.StatusBadRequest)
			return
		}
		serverName := parts[0]
		if err := mcpHost.RestartServer(serverName); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status": "restarted",
			"server": serverName,
		})
	})

	// --- API Documentation ---

	mux.HandleFunc("/api/docs", handleAPIDocs)
	mux.HandleFunc("/api/spec", handleAPISpec(cfg))

	// --- Voice Engine ---

	mux.HandleFunc("/api/voice/transcribe", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		if voiceEngine == nil || voiceEngine.stt == nil {
			http.Error(w, `{"error":"voice stt not enabled"}`, http.StatusServiceUnavailable)
			return
		}

		// Parse multipart form.
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"parse form: %v"}`, err), http.StatusBadRequest)
			return
		}

		// Get audio file.
		file, header, err := r.FormFile("audio")
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"missing audio field: %v"}`, err), http.StatusBadRequest)
			return
		}
		defer file.Close()

		// Get options from form.
		language := r.FormValue("language")
		format := r.FormValue("format")
		if format == "" {
			// Try to infer from filename.
			if strings.HasSuffix(header.Filename, ".ogg") {
				format = "ogg"
			} else if strings.HasSuffix(header.Filename, ".wav") {
				format = "wav"
			} else if strings.HasSuffix(header.Filename, ".webm") {
				format = "webm"
			} else {
				format = "mp3"
			}
		}

		opts := STTOptions{
			Language: language,
			Format:   format,
		}

		// Transcribe.
		result, err := voiceEngine.Transcribe(r.Context(), file, opts)
		if err != nil {
			logErrorCtx(r.Context(), "voice transcribe failed", "error", err)
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	})

	mux.HandleFunc("/api/voice/synthesize", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		if voiceEngine == nil || voiceEngine.tts == nil {
			http.Error(w, `{"error":"voice tts not enabled"}`, http.StatusServiceUnavailable)
			return
		}

		// Parse request body.
		var req struct {
			Text   string  `json:"text"`
			Voice  string  `json:"voice,omitempty"`
			Speed  float64 `json:"speed,omitempty"`
			Format string  `json:"format,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"invalid json: %v"}`, err), http.StatusBadRequest)
			return
		}
		if req.Text == "" {
			http.Error(w, `{"error":"text field required"}`, http.StatusBadRequest)
			return
		}

		opts := TTSOptions{
			Voice:  req.Voice,
			Speed:  req.Speed,
			Format: req.Format,
		}

		// Synthesize.
		stream, err := voiceEngine.Synthesize(r.Context(), req.Text, opts)
		if err != nil {
			logErrorCtx(r.Context(), "voice synthesize failed", "error", err)
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		defer stream.Close()

		// Determine content type.
		format := req.Format
		if format == "" {
			format = cfg.Voice.TTS.Format
		}
		if format == "" {
			format = "mp3"
		}
		contentType := "audio/mpeg"
		if format == "opus" {
			contentType = "audio/opus"
		} else if format == "wav" {
			contentType = "audio/wav"
		}

		// Stream audio to response.
		w.Header().Set("Content-Type", contentType)
		io.Copy(w, stream)
	})

	// --- Cost Estimate ---

	mux.HandleFunc("/dispatch/estimate", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		var tasks []Task
		if err := json.NewDecoder(r.Body).Decode(&tasks); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
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
		tasks := listFailedTasks(state)
		if tasks == nil {
			tasks = []failedTaskInfo{}
		}
		json.NewEncoder(w).Encode(tasks)
	})

	mux.HandleFunc("/dispatch/", func(w http.ResponseWriter, r *http.Request) {
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
			if state.broker == nil {
				http.Error(w, `{"error":"streaming not available"}`, http.StatusServiceUnavailable)
				return
			}
			serveSSE(w, r, state.broker, taskID)
			return
		}

		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		switch action {
		case "retry":
			result, err := retryTask(r.Context(), cfg, taskID, state, sem)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusNotFound)
				return
			}
			auditLog(cfg.HistoryDB, "task.retry", "http",
				fmt.Sprintf("original=%s status=%s", taskID, result.Status), clientIP(r))
			json.NewEncoder(w).Encode(result)

		case "reroute":
			result, err := rerouteTask(r.Context(), cfg, taskID, state, sem)
			if err != nil {
				status := http.StatusNotFound
				if strings.Contains(err.Error(), "not enabled") {
					status = http.StatusBadRequest
				}
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), status)
				return
			}
			auditLog(cfg.HistoryDB, "task.reroute", "http",
				fmt.Sprintf("original=%s role=%s status=%s", taskID, result.Route.Role, result.Task.Status), clientIP(r))
			json.NewEncoder(w).Encode(result)

		default:
			http.Error(w, `{"error":"unknown action, use retry, reroute, or stream"}`, http.StatusBadRequest)
		}
	})

	// --- Smart Dispatch Route ---

	mux.HandleFunc("/route/classify", func(w http.ResponseWriter, r *http.Request) {
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
		route := routeTask(r.Context(), cfg, RouteRequest{Prompt: body.Prompt, Source: "http"}, sem)
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
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"enabled":     cfg.SmartDispatch.Enabled,
				"coordinator": cfg.SmartDispatch.Coordinator,
				"defaultRole": cfg.SmartDispatch.DefaultRole,
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
		auditLog(cfg.HistoryDB, "route.request", "http",
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

			routeTraceID := traceIDFromContext(r.Context())
			go func() {
				routeCtx := withTraceID(context.Background(), routeTraceID)
				result := smartDispatch(routeCtx, cfg, body.Prompt, "http", state, sem)
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
		result := smartDispatch(r.Context(), cfg, body.Prompt, "http", state, sem)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	})

	// --- Budget ---

	mux.HandleFunc("/budget", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		status := queryBudgetStatus(cfg)
		json.NewEncoder(w).Encode(status)
	})

	mux.HandleFunc("/budget/pause", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		cfg.Budgets.Paused = true
		configPath := filepath.Join(cfg.baseDir, "config.json")
		if err := setBudgetPaused(configPath, true); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		auditLog(cfg.HistoryDB, "budget.pause", "http", "all paid execution paused", clientIP(r))
		logWarn("budget PAUSED by API request", "ip", clientIP(r))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"paused"}`))
	})

	mux.HandleFunc("/budget/resume", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		cfg.Budgets.Paused = false
		configPath := filepath.Join(cfg.baseDir, "config.json")
		if err := setBudgetPaused(configPath, false); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		auditLog(cfg.HistoryDB, "budget.resume", "http", "paid execution resumed", clientIP(r))
		logInfo("budget RESUMED by API request", "ip", clientIP(r))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"active"}`))
	})

	// --- Audit Log ---
	// --- Trust Gradient ---
	mux.HandleFunc("/trust", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		statuses := getAllTrustStatuses(cfg)
		json.NewEncoder(w).Encode(statuses)
	})

	mux.HandleFunc("/trust/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		roleName := strings.TrimPrefix(r.URL.Path, "/trust/")
		roleName = strings.TrimSuffix(roleName, "/")
		if roleName == "" {
			http.Error(w, `{"error":"role name required"}`, http.StatusBadRequest)
			return
		}

		// Check if role exists.
		if _, ok := cfg.Roles[roleName]; !ok {
			http.Error(w, `{"error":"role not found"}`, http.StatusNotFound)
			return
		}

		switch r.Method {
		case http.MethodGet:
			status := getTrustStatus(cfg, roleName)
			json.NewEncoder(w).Encode(status)

		case http.MethodPost, http.MethodPut:
			var body struct {
				Level string `json:"level"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
				return
			}
			if !isValidTrustLevel(body.Level) {
				http.Error(w, fmt.Sprintf(`{"error":"invalid level, valid: %s"}`,
					strings.Join(validTrustLevels, ", ")), http.StatusBadRequest)
				return
			}

			oldLevel := resolveTrustLevel(cfg, roleName)
			if err := updateRoleTrustLevel(cfg, roleName, body.Level); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
				return
			}

			// Persist to config.json.
			configPath := filepath.Join(cfg.baseDir, "config.json")
			if err := saveRoleTrustLevel(configPath, roleName, body.Level); err != nil {
				logWarn("persist trust level failed", "role", roleName, "error", err)
			}

			// Record trust event.
			recordTrustEvent(cfg.HistoryDB, roleName, "set", oldLevel, body.Level, 0,
				"set via API")

			auditLog(cfg.HistoryDB, "trust.set", "http",
				fmt.Sprintf("role=%s from=%s to=%s", roleName, oldLevel, body.Level), clientIP(r))

			json.NewEncoder(w).Encode(getTrustStatus(cfg, roleName))

		default:
			http.Error(w, `{"error":"GET or POST"}`, http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/trust-events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		role := r.URL.Query().Get("role")
		limit := 20
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 {
				limit = n
			}
		}
		events, err := queryTrustEvents(cfg.HistoryDB, role, limit)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if events == nil {
			events = []map[string]any{}
		}
		json.NewEncoder(w).Encode(events)
	})

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
		result := handleIncomingWebhook(ctx, cfg, name, r, state, sem)
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
			Name     string `json:"name"`
			Role     string `json:"role"`
			Enabled  bool   `json:"enabled"`
			Template string `json:"template,omitempty"`
			Filter   string `json:"filter,omitempty"`
			Workflow string `json:"workflow,omitempty"`
			HasSecret bool  `json:"hasSecret"`
		}
		var list []webhookInfo
		for name, wh := range cfg.IncomingWebhooks {
			list = append(list, webhookInfo{
				Name:      name,
				Role:      wh.Role,
				Enabled:   wh.isEnabled(),
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

		entries, total, err := queryAuditLog(cfg.HistoryDB, limit, offset)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if entries == nil {
			entries = []AuditEntry{}
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
					"history": retentionDays(cfg.Retention.History, 90),
					"sessions": retentionDays(cfg.Retention.Sessions, 30),
					"auditLog": retentionDays(cfg.Retention.AuditLog, 365),
					"logs": retentionDays(cfg.Retention.Logs, 14),
					"workflows": retentionDays(cfg.Retention.Workflows, 90),
					"reflections": retentionDays(cfg.Retention.Reflections, 60),
					"sla": retentionDays(cfg.Retention.SLA, 90),
					"trustEvents": retentionDays(cfg.Retention.TrustEvents, 90),
					"handoffs": retentionDays(cfg.Retention.Handoffs, 60),
					"queue": retentionDays(cfg.Retention.Queue, 7),
					"versions": retentionDays(cfg.Retention.Versions, 180),
					"outputs": retentionDays(cfg.Retention.Outputs, 30),
					"uploads": retentionDays(cfg.Retention.Uploads, 7),
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
		auditLog(cfg.HistoryDB, "retention.cleanup", "http", "", clientIP(r))
		results := runRetention(cfg)
		json.NewEncoder(w).Encode(map[string]any{"results": results})
	})

	mux.HandleFunc("/data/export", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		auditLog(cfg.HistoryDB, "data.export", "http", "", clientIP(r))
		data, err := exportData(cfg)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
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
		auditLog(cfg.HistoryDB, "data.purge", "http", "before="+before, clientIP(r))
		results, err := purgeDataBefore(cfg, before)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
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

		auditLog(cfg.HistoryDB, "backup.download", "http", "", clientIP(r))

		// Create temp backup.
		tmpFile, err := os.CreateTemp("", "tetora-backup-*.tar.gz")
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"create temp: %v"}`, err), http.StatusInternalServerError)
			return
		}
		tmpPath := tmpFile.Name()
		tmpFile.Close()
		defer os.Remove(tmpPath)

		if err := createBackup(cfg.baseDir, tmpPath); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"create backup: %v"}`, err), http.StatusInternalServerError)
			return
		}

		data, err := os.ReadFile(tmpPath)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"read backup: %v"}`, err), http.StatusInternalServerError)
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
			if limiter.isLocked(ip) {
				auditLog(cfg.HistoryDB, "dashboard.login.ratelimit", "http", "", ip)
				if secMon != nil {
					secMon.recordEvent(ip, "login.ratelimit")
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
				limiter.recordFailure(ip)
				auditLog(cfg.HistoryDB, "dashboard.login.fail", "http", "", ip)
				if secMon != nil {
					secMon.recordEvent(ip, "login.fail")
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(dashboardLoginFailHTML))
				return
			}

			// Success — clear rate limit.
			limiter.recordSuccess(ip)

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
			if cfg.tlsEnabled {
				cookie.Secure = true
			}
			http.SetCookie(w, cookie)
			auditLog(cfg.HistoryDB, "dashboard.login", "http", "", ip)
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
			versions, err := queryVersions(cfg.HistoryDB, "config", "config.json", limit)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
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
			configPath := filepath.Join(cfg.baseDir, "config.json")
			if err := snapshotConfig(cfg.HistoryDB, configPath, "api", req.Reason); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
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
			ver, err := queryVersionByID(cfg.HistoryDB, path)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(ver)
			return
		}

		// POST /config/versions/{id}/restore
		if r.Method == http.MethodPost && strings.HasSuffix(path, "/restore") {
			versionID := strings.TrimSuffix(path, "/restore")
			configPath := filepath.Join(cfg.baseDir, "config.json")
			if _, err := restoreConfigVersion(cfg.HistoryDB, configPath, versionID); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
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
			result, err := versionDiffDetail(cfg.HistoryDB, parts[0], parts[1])
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
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
			entities, err := queryAllVersionedEntities(cfg.HistoryDB)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if entities == nil {
				entities = []ConfigVersion{}
			}
			json.NewEncoder(w).Encode(entities)
			return
		}
		versions, err := queryVersions(cfg.HistoryDB, entityType, entityName, limit)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if versions == nil {
			versions = []ConfigVersion{}
		}
		json.NewEncoder(w).Encode(versions)
	})

	// PWA assets.
	mux.HandleFunc("/dashboard/manifest.json", handlePWAManifest)
	mux.HandleFunc("/dashboard/sw.js", handlePWAServiceWorker)
	mux.HandleFunc("/dashboard/icon.svg", handlePWAIcon)

	// Dashboard.
	mux.HandleFunc("/dashboard", handleDashboard)

	// Middleware chain: trace → rate limit → dashboard auth → IP allowlist → API auth → mux
	handler := traceMiddleware(rateLimitMiddleware(cfg, apiLimiter,
		dashboardAuthMiddleware(cfg,
			ipAllowlistMiddleware(allowlist, cfg.HistoryDB,
				authMiddleware(cfg, secMon, mux)))))

	srv := &http.Server{Addr: addr, Handler: handler}

	// Periodic cleanup for rate limiters + security monitor + async route results + failed tasks.
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			limiter.cleanup()
			apiLimiter.cleanup()
			if secMon != nil {
				secMon.cleanup()
			}
			cleanupRouteResults()
			cleanupFailedTasks(state)
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
		logInfo("https server listening", "addr", addr)
	} else {
		go func() {
			if err := srv.ListenAndServe(); err != http.ErrServerClosed {
				logError("http server error", "error", err)
			}
		}()
		logInfo("http server listening", "addr", addr)
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
