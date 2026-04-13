package httpapi_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"tetora/internal/httpapi"
)

// ---------------------------------------------------------------------------
// IsValidClientID
// ---------------------------------------------------------------------------

func TestIsValidClientID_Valid(t *testing.T) {
	cases := []string{
		"cli_abc",
		"cli_abc123",
		"cli_my-client",
		"cli_" + "a",                    // minimum body length (1)
		"cli_" + repeat('a', 28),        // maximum body length (28)
		"cli_0",
		"cli_a-b-c",
	}
	for _, id := range cases {
		if !httpapi.IsValidClientID(id) {
			t.Errorf("IsValidClientID(%q) = false, want true", id)
		}
	}
}

func TestIsValidClientID_Invalid(t *testing.T) {
	cases := []struct {
		id   string
		desc string
	}{
		{"", "empty"},
		{"cli_", "no body after prefix (length 4, minimum 5)"},
		{"cli_" + repeat('a', 29), "body too long (29 chars)"},
		{"abc_foo", "wrong prefix"},
		{"CLI_abc", "uppercase prefix"},
		{"cli_ABC", "uppercase in body"},
		{"cli_foo bar", "space in body"},
		{"cli_foo/bar", "slash in body"},
		{"cli_foo.bar", "dot in body"},
	}
	for _, tc := range cases {
		if httpapi.IsValidClientID(tc.id) {
			t.Errorf("IsValidClientID(%q) [%s] = true, want false", tc.id, tc.desc)
		}
	}
}

func TestIsValidClientID_BoundaryLength(t *testing.T) {
	// Exactly 5 chars (cli_ + 1): valid.
	if !httpapi.IsValidClientID("cli_a") {
		t.Error("IsValidClientID(cli_a) = false, want true")
	}
	// Exactly 32 chars (cli_ + 28): valid.
	id32 := "cli_" + repeat('a', 28)
	if !httpapi.IsValidClientID(id32) {
		t.Errorf("IsValidClientID(%q) = false, want true", id32)
	}
	// 33 chars (cli_ + 29): invalid.
	id33 := "cli_" + repeat('a', 29)
	if httpapi.IsValidClientID(id33) {
		t.Errorf("IsValidClientID(%q) = true, want false", id33)
	}
}

// ---------------------------------------------------------------------------
// LoginLimiter
// ---------------------------------------------------------------------------

func TestLoginLimiter_NotLockedInitially(t *testing.T) {
	ll := httpapi.NewLoginLimiter(context.Background())
	if ll.IsLocked("1.2.3.4") {
		t.Error("IsLocked on fresh limiter = true, want false")
	}
}

func TestLoginLimiter_LockedAfterMaxFailures(t *testing.T) {
	ll := httpapi.NewLoginLimiter(context.Background())
	ip := "10.0.0.1"
	for i := 0; i < 5; i++ {
		ll.RecordFailure(ip)
	}
	if !ll.IsLocked(ip) {
		t.Errorf("IsLocked after 5 failures = false, want true")
	}
}

func TestLoginLimiter_NotLockedBeforeMaxFailures(t *testing.T) {
	ll := httpapi.NewLoginLimiter(context.Background())
	ip := "10.0.0.2"
	for i := 0; i < 4; i++ {
		ll.RecordFailure(ip)
	}
	if ll.IsLocked(ip) {
		t.Errorf("IsLocked after 4 failures = true, want false")
	}
}

func TestLoginLimiter_RecordSuccess_ClearsLock(t *testing.T) {
	ll := httpapi.NewLoginLimiter(context.Background())
	ip := "10.0.0.3"
	for i := 0; i < 5; i++ {
		ll.RecordFailure(ip)
	}
	ll.RecordSuccess(ip)
	if ll.IsLocked(ip) {
		t.Error("IsLocked after RecordSuccess = true, want false")
	}
}

func TestLoginLimiter_DifferentIPsAreIndependent(t *testing.T) {
	ll := httpapi.NewLoginLimiter(context.Background())
	ip1, ip2 := "10.0.0.4", "10.0.0.5"
	for i := 0; i < 5; i++ {
		ll.RecordFailure(ip1)
	}
	if ll.IsLocked(ip2) {
		t.Error("IsLocked for unrelated IP = true, want false")
	}
}

func TestLoginLimiter_Cleanup_RemovesExpiredEntries(t *testing.T) {
	// We cannot fake the clock in the production type without internal access,
	// so we test the public surface: Cleanup() must not panic and must be callable
	// concurrently with RecordFailure/IsLocked.
	ll := httpapi.NewLoginLimiter(context.Background())
	ip := "10.0.0.6"
	ll.RecordFailure(ip)
	// Cleanup should be safe to call explicitly even though the constructor
	// already started the background ticker.
	ll.Cleanup()
	// After cleanup the entry should still exist (not yet expired by clock).
	// We just verify no panic / data race.
}

// ---------------------------------------------------------------------------
// IPAllowlist
// ---------------------------------------------------------------------------

func TestIPAllowlist_NilAllowsAll(t *testing.T) {
	var al *httpapi.IPAllowlist
	if !al.Contains("1.2.3.4") {
		t.Error("nil IPAllowlist.Contains = false, want true")
	}
	if !al.Contains("::1") {
		t.Error("nil IPAllowlist.Contains(ipv6) = false, want true")
	}
}

func TestIPAllowlist_ContainsExactIP(t *testing.T) {
	al := httpapi.ParseAllowlist([]string{"192.168.1.10", "10.0.0.1"})
	if !al.Contains("192.168.1.10") {
		t.Error("Contains(192.168.1.10) = false, want true")
	}
	if !al.Contains("10.0.0.1") {
		t.Error("Contains(10.0.0.1) = false, want true")
	}
	if al.Contains("192.168.1.11") {
		t.Error("Contains(192.168.1.11) = true, want false")
	}
}

func TestIPAllowlist_ContainsCIDR(t *testing.T) {
	al := httpapi.ParseAllowlist([]string{"10.0.0.0/8"})
	if !al.Contains("10.255.255.255") {
		t.Error("Contains(10.255.255.255) in 10/8 = false, want true")
	}
	if al.Contains("11.0.0.1") {
		t.Error("Contains(11.0.0.1) not in 10/8 = true, want false")
	}
}

func TestIPAllowlist_EmptyListNil(t *testing.T) {
	// ParseAllowlist(nil) returns nil — allow all.
	al := httpapi.ParseAllowlist(nil)
	if al != nil {
		t.Error("ParseAllowlist(nil) should return nil (allow-all sentinel)")
	}
	if !al.Contains("1.2.3.4") {
		t.Error("nil allowlist should allow any IP")
	}
}

func TestIPAllowlist_InvalidIPReturnsFalse(t *testing.T) {
	al := httpapi.ParseAllowlist([]string{"10.0.0.1"})
	if al.Contains("not-an-ip") {
		t.Error("Contains(not-an-ip) = true, want false")
	}
}

// ---------------------------------------------------------------------------
// APIRateLimiter
// ---------------------------------------------------------------------------

func TestAPIRateLimiter_AllowsUpToLimit(t *testing.T) {
	rl := httpapi.NewAPIRateLimiter(context.Background(), 5)
	ip := "10.1.1.1"
	for i := 0; i < 5; i++ {
		if !rl.Allow(ip) {
			t.Errorf("Allow call %d = false, want true", i+1)
		}
	}
}

func TestAPIRateLimiter_DeniesOverLimit(t *testing.T) {
	rl := httpapi.NewAPIRateLimiter(context.Background(), 3)
	ip := "10.1.1.2"
	for i := 0; i < 3; i++ {
		rl.Allow(ip)
	}
	if rl.Allow(ip) {
		t.Error("Allow after limit exceeded = true, want false")
	}
}

func TestAPIRateLimiter_DifferentIPsAreIndependent(t *testing.T) {
	rl := httpapi.NewAPIRateLimiter(context.Background(), 2)
	ip1, ip2 := "10.1.1.3", "10.1.1.4"
	rl.Allow(ip1)
	rl.Allow(ip1)
	// ip1 is now at limit; ip2 should still be allowed.
	if !rl.Allow(ip2) {
		t.Error("Allow for fresh IP = false, want true")
	}
}

func TestAPIRateLimiter_DefaultLimitApplied(t *testing.T) {
	// maxPerMin <= 0 should default to 60.
	rl := httpapi.NewAPIRateLimiter(context.Background(), 0)
	// First call must be allowed.
	if !rl.Allow("10.1.1.5") {
		t.Error("Allow(first request with default limit) = false, want true")
	}
}

func TestAPIRateLimiter_Cleanup_NoPanic(t *testing.T) {
	rl := httpapi.NewAPIRateLimiter(context.Background(), 10)
	rl.Allow("10.1.1.6")
	rl.Cleanup() // should not panic
}

// ---------------------------------------------------------------------------
// ValidateDashboardCookie
// ---------------------------------------------------------------------------

func TestValidateDashboardCookie_Valid(t *testing.T) {
	secret := "test-secret-42"
	cookie := httpapi.DashboardAuthCookie(secret)
	if !httpapi.ValidateDashboardCookie(cookie, secret) {
		t.Errorf("ValidateDashboardCookie(fresh cookie) = false, want true")
	}
}

func TestValidateDashboardCookie_Expired(t *testing.T) {
	secret := "test-secret-42"
	// Craft a cookie timestamped 25 hours ago.
	ts := fmt.Sprintf("%d", time.Now().Add(-25*time.Hour).Unix())
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	sig := hex.EncodeToString(mac.Sum(nil))
	cookie := ts + ":" + sig
	if httpapi.ValidateDashboardCookie(cookie, secret) {
		t.Error("ValidateDashboardCookie(expired) = true, want false")
	}
}

func TestValidateDashboardCookie_TamperedSignature(t *testing.T) {
	secret := "test-secret-42"
	ts := fmt.Sprintf("%d", time.Now().Unix())
	wrongMac := hmac.New(sha256.New, []byte("wrong-secret"))
	wrongMac.Write([]byte(ts))
	wrongSig := hex.EncodeToString(wrongMac.Sum(nil))
	cookie := ts + ":" + wrongSig
	if httpapi.ValidateDashboardCookie(cookie, secret) {
		t.Error("ValidateDashboardCookie(tampered sig) = true, want false")
	}
}

func TestValidateDashboardCookie_Malformed(t *testing.T) {
	if httpapi.ValidateDashboardCookie("no-colon-here", "secret") {
		t.Error("ValidateDashboardCookie(malformed) = true, want false")
	}
}

func TestValidateDashboardCookie_Empty(t *testing.T) {
	if httpapi.ValidateDashboardCookie("", "secret") {
		t.Error("ValidateDashboardCookie(empty) = true, want false")
	}
}

func TestValidateDashboardCookie_WrongSecret(t *testing.T) {
	cookie := httpapi.DashboardAuthCookie("correct-secret")
	if httpapi.ValidateDashboardCookie(cookie, "wrong-secret") {
		t.Error("ValidateDashboardCookie(wrong secret) = true, want false")
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func repeat(c byte, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = c
	}
	return string(b)
}
