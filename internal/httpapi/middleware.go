package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"tetora/internal/httputil"
	"tetora/internal/log"
)

// IsValidOutputFilename checks that a filename contains only safe characters.
// Allowed: alphanumeric, dash, underscore, dot. No path separators or encoded chars.
func IsValidOutputFilename(name string) bool {
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

// JSONError writes a JSON-encoded error response. Using json.Marshal to encode
// the message ensures the output is valid JSON even when the message contains
// quotes, backslashes, or other characters that would break a raw fmt.Sprintf.
func JSONError(w http.ResponseWriter, msg string, code int) {
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

// --- Dashboard auth cookie helpers ---

// DashboardAuthCookie generates a signed cookie value for dashboard auth.
func DashboardAuthCookie(secret string) string {
	// Sign a timestamp-based token: timestamp:hmac(timestamp, secret)
	ts := fmt.Sprintf("%d", time.Now().Unix())
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	sig := hex.EncodeToString(mac.Sum(nil))
	return ts + ":" + sig
}

// ValidateDashboardCookie checks if a dashboard auth cookie is valid and not expired (24h).
func ValidateDashboardCookie(cookie, secret string) bool {
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

// --- Multi-tenant client identification ---

// ClientContextKey is the type used for context value keys to avoid collisions.
type ClientContextKey string

// ClientIDContextKey is the context key for the client ID.
const ClientIDContextKey ClientContextKey = "clientID"

// ClientFromRequest extracts the client ID from the request.
// Falls back to the default client ID from config if not provided.
func ClientFromRequest(r *http.Request, defaultID string) string {
	if id := r.Header.Get("X-Client-ID"); id != "" {
		return id
	}
	return defaultID
}

// IsValidClientID validates that a client ID matches the expected format: cli_ prefix
// followed by 1-28 lowercase alphanumeric or hyphen characters.
func IsValidClientID(id string) bool {
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

// ClientMiddleware extracts X-Client-ID from the request header, validates it,
// and stores it in the request context. If absent, uses the config default.
func ClientMiddleware(defaultClientID string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientID := ClientFromRequest(r, defaultClientID)
		if !IsValidClientID(clientID) {
			http.Error(w, `{"error":"invalid client id"}`, http.StatusBadRequest)
			return
		}
		ctx := context.WithValue(r.Context(), ClientIDContextKey, clientID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GetClientID extracts the client ID from request context (set by ClientMiddleware).
func GetClientID(r *http.Request) string {
	if id, ok := r.Context().Value(ClientIDContextKey).(string); ok {
		return id
	}
	return "cli_default"
}

// --- Login Rate Limiter ---

type loginAttempt struct {
	failures int
	lastFail time.Time
}

// LoginLimiter tracks login failure attempts per IP to implement rate limiting.
type LoginLimiter struct {
	mu       sync.Mutex
	attempts map[string]*loginAttempt
}

const (
	loginMaxFailures = 5
	loginLockoutDur  = 15 * time.Minute
)

// NewLoginLimiter creates a new login rate limiter.
// It starts a background goroutine that calls Cleanup every 5 minutes.
// The goroutine runs until the process exits.
func NewLoginLimiter() *LoginLimiter {
	ll := &LoginLimiter{attempts: make(map[string]*loginAttempt)}
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			ll.Cleanup()
		}
	}()
	return ll
}

// IsLocked returns true if the IP is currently locked out.
func (ll *LoginLimiter) IsLocked(ip string) bool {
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

// RecordFailure records a failed login attempt for the given IP.
func (ll *LoginLimiter) RecordFailure(ip string) {
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

// RecordSuccess clears the failure count for the given IP.
func (ll *LoginLimiter) RecordSuccess(ip string) {
	ll.mu.Lock()
	defer ll.mu.Unlock()
	delete(ll.attempts, ip)
}

// Cleanup removes expired entries. Called periodically to prevent memory leak.
func (ll *LoginLimiter) Cleanup() {
	ll.mu.Lock()
	defer ll.mu.Unlock()
	for ip, a := range ll.attempts {
		if time.Since(a.lastFail) >= loginLockoutDur {
			delete(ll.attempts, ip)
		}
	}
}

// --- IP Allowlist ---

// IPAllowlist holds parsed IP addresses and CIDR ranges for request filtering.
type IPAllowlist struct {
	ips   []net.IP
	cidrs []*net.IPNet
}

// ParseAllowlist parses a list of IP addresses and CIDR ranges into an IPAllowlist.
// Returns nil if the list is empty (meaning all IPs are allowed).
func ParseAllowlist(entries []string) *IPAllowlist {
	if len(entries) == 0 {
		return nil
	}
	al := &IPAllowlist{}
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

// Contains reports whether the IP string is allowed by this allowlist.
// A nil receiver always returns true (no allowlist = allow all).
func (al *IPAllowlist) Contains(ipStr string) bool {
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

// IPAllowlistMiddleware rejects requests from IPs not in the allowlist.
// If allowlist is nil, all IPs are allowed (backward compatible).
// blockedFn is called for blocked requests (e.g. to log/audit); it receives (path, ip).
// If blockedFn is nil, blocking is silent.
func IPAllowlistMiddleware(al *IPAllowlist, blockedFn func(path, ip string), next http.Handler) http.Handler {
	if al == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always allow healthz and metrics for monitoring probes.
		if r.URL.Path == "/healthz" || r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}

		ip := httputil.ClientIP(r)
		if !al.Contains(ip) {
			if blockedFn != nil {
				blockedFn(r.URL.Path, ip)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"error":"forbidden"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- API Rate Limiter ---

// APIRateLimiter implements a sliding-window per-IP rate limiter.
type APIRateLimiter struct {
	mu      sync.Mutex
	windows map[string]*ipWindow
	limit   int // max requests per minute
}

type ipWindow struct {
	timestamps []time.Time
}

// NewAPIRateLimiter creates a new rate limiter allowing up to maxPerMin requests per minute per IP.
// It starts a background goroutine that calls Cleanup every minute.
// The goroutine runs until the process exits.
func NewAPIRateLimiter(maxPerMin int) *APIRateLimiter {
	if maxPerMin <= 0 {
		maxPerMin = 60
	}
	rl := &APIRateLimiter{
		windows: make(map[string]*ipWindow),
		limit:   maxPerMin,
	}
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			rl.Cleanup()
		}
	}()
	return rl
}

// Allow checks if the IP is under the rate limit. Returns true if the request is allowed.
func (rl *APIRateLimiter) Allow(ip string) bool {
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

// Cleanup removes expired entries to prevent memory leak.
func (rl *APIRateLimiter) Cleanup() {
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

// --- Security Monitor ---

// SecurityMonitor tracks security-related events and sends alerts
// when suspicious patterns are detected (e.g. auth failure bursts).
type SecurityMonitor struct {
	mu            sync.Mutex
	events        map[string][]time.Time // ip -> event timestamps
	lastAlert     map[string]time.Time   // ip -> last alert time (dedup)
	threshold     int                    // number of failures to trigger alert
	windowMin     int                    // window in minutes
	alertCooldown time.Duration          // min time between alerts for same IP
	notifyFn      func(string)           // notification callback
}

// NewSecurityMonitor creates a security monitor.
// Returns nil if disabled or notifyFn is nil (safe to call methods on nil).
// When non-nil, it starts a background goroutine that calls Cleanup every 5 minutes.
// The goroutine runs until the process exits.
func NewSecurityMonitor(enabled bool, failThreshold, failWindowMin int, notifyFn func(string)) *SecurityMonitor {
	if !enabled || notifyFn == nil {
		return nil
	}
	threshold := failThreshold
	if threshold <= 0 {
		threshold = 10
	}
	windowMin := failWindowMin
	if windowMin <= 0 {
		windowMin = 5
	}
	sm := &SecurityMonitor{
		events:        make(map[string][]time.Time),
		lastAlert:     make(map[string]time.Time),
		threshold:     threshold,
		windowMin:     windowMin,
		alertCooldown: 15 * time.Minute,
		notifyFn:      notifyFn,
	}
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			sm.Cleanup()
		}
	}()
	return sm
}

// RecordEvent records a security event for the given IP.
// If the event count exceeds the threshold within the window, an alert is sent.
func (sm *SecurityMonitor) RecordEvent(ip, eventType string) {
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

// Cleanup removes expired entries to prevent memory leak.
func (sm *SecurityMonitor) Cleanup() {
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

// --- Pure middleware (no config dependency) ---

// RecoveryMiddleware catches panics in HTTP handlers, logs the stack trace, and returns 500.
func RecoveryMiddleware(next http.Handler) http.Handler {
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

// BodySizeMiddleware limits request body size to prevent resource exhaustion (10 MB).
func BodySizeMiddleware(next http.Handler) http.Handler {
	const maxBodySize = 10 << 20 // 10 MB
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBodySize)
		}
		next.ServeHTTP(w, r)
	})
}
