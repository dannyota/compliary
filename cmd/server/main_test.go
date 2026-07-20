package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// okHandler is a trivial handler that records that it was reached.
func okHandler(reached *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*reached = true
		w.WriteHeader(http.StatusOK)
	})
}

func TestClientIP_XFFSpoofResistant(t *testing.T) {
	// Edge proxies (CloudFront) APPEND the viewer IP to X-Forwarded-For, so the
	// trustworthy client IP is the entry the edge added — position len-hops
	// (hops=1 here). The leftmost entry is client-controllable and must never
	// be used for rate limiting.
	tests := []struct {
		name       string
		xff        string
		remoteAddr string
		trustProxy bool
		want       string
	}{
		{
			// Client spoofs a leftmost value; CloudFront appends the real IP.
			// We must take the appended (rightmost) value, ignoring the spoof.
			name:       "trust_proxy_ignores_spoofed_leftmost",
			xff:        "1.2.3.4, 203.0.113.7",
			remoteAddr: "10.0.0.5:443",
			trustProxy: true,
			want:       "203.0.113.7",
		},
		{
			// Different spoofed leftmost, SAME edge-appended IP → same bucket.
			name:       "trust_proxy_spoof_rotation_same_bucket",
			xff:        "9.9.9.9, 203.0.113.7",
			remoteAddr: "10.0.0.5:443",
			trustProxy: true,
			want:       "203.0.113.7",
		},
		{
			name:       "trust_proxy_single_entry",
			xff:        "203.0.113.9",
			remoteAddr: "10.0.0.5:443",
			trustProxy: true,
			want:       "203.0.113.9",
		},
		{
			name:       "no_trust_proxy_uses_remoteaddr",
			xff:        "203.0.113.7, 70.132.60.1",
			remoteAddr: "192.0.2.44:5555",
			trustProxy: false,
			want:       "192.0.2.44",
		},
		{
			name:       "trust_proxy_no_xff_falls_back",
			xff:        "",
			remoteAddr: "198.51.100.2:1234",
			trustProxy: true,
			want:       "198.51.100.2",
		},
		{
			name:       "trust_proxy_trailing_space",
			xff:        "1.2.3.4,  203.0.113.7 ",
			remoteAddr: "10.0.0.5:443",
			trustProxy: true,
			want:       "203.0.113.7",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/oauth/authorize", nil)
			r.RemoteAddr = tc.remoteAddr
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			got := clientIP(r, tc.trustProxy)
			if got != tc.want {
				t.Errorf("clientIP() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOAuthEndpointLimit_BruteForceGate(t *testing.T) {
	const perMin = 5
	rl := newRateLimiter(float64(perMin)/60.0, perMin, false)
	var reached bool
	h := oauthEndpointLimit(okHandler(&reached), rl)

	// N allowed on /oauth/authorize from one IP.
	for i := 0; i < perMin; i++ {
		reached = false
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/oauth/authorize", nil)
		r.RemoteAddr = "203.0.113.10:5000"
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("attempt %d: got %d, want 200", i+1, w.Code)
		}
	}

	// N+1 → 429 with Retry-After.
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/oauth/authorize", nil)
	r.RemoteAddr = "203.0.113.10:5000"
	h.ServeHTTP(w, r)
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("attempt %d: got %d, want 429", perMin+1, w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("429 response missing Retry-After header")
	}
}

func TestOAuthEndpointLimit_TokenEndpoint(t *testing.T) {
	const perMin = 3
	rl := newRateLimiter(float64(perMin)/60.0, perMin, false)
	var reached bool
	h := oauthEndpointLimit(okHandler(&reached), rl)

	for i := 0; i < perMin; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/oauth/token", nil)
		r.RemoteAddr = "203.0.113.20:5000"
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("token attempt %d: got %d, want 200", i+1, w.Code)
		}
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/oauth/token", nil)
	r.RemoteAddr = "203.0.113.20:5000"
	h.ServeHTTP(w, r)
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("token attempt %d: got %d, want 429", perMin+1, w.Code)
	}
}

func TestOAuthEndpointLimit_SeparateBucketsPerIP(t *testing.T) {
	const perMin = 2
	rl := newRateLimiter(float64(perMin)/60.0, perMin, false)
	var reached bool
	h := oauthEndpointLimit(okHandler(&reached), rl)

	// Exhaust IP A.
	for i := 0; i < perMin; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/oauth/authorize", nil)
		r.RemoteAddr = "203.0.113.30:5000"
		h.ServeHTTP(w, r)
	}
	// IP A is now blocked.
	wA := httptest.NewRecorder()
	rA := httptest.NewRequest("POST", "/oauth/authorize", nil)
	rA.RemoteAddr = "203.0.113.30:5000"
	h.ServeHTTP(wA, rA)
	if wA.Code != http.StatusTooManyRequests {
		t.Errorf("IP A over limit: got %d, want 429", wA.Code)
	}

	// IP B has its own fresh bucket.
	wB := httptest.NewRecorder()
	rB := httptest.NewRequest("POST", "/oauth/authorize", nil)
	rB.RemoteAddr = "203.0.113.31:5000"
	h.ServeHTTP(wB, rB)
	if wB.Code != http.StatusOK {
		t.Errorf("IP B fresh bucket: got %d, want 200", wB.Code)
	}
}

func TestOAuthEndpointLimit_OnlyAuthEndpoints(t *testing.T) {
	// A tiny limiter that would block after 1 request — but non-OAuth-POST
	// paths and GET requests must pass through untouched.
	rl := newRateLimiter(0.001, 1, false)
	var reached bool
	h := oauthEndpointLimit(okHandler(&reached), rl)

	passthrough := []struct {
		method string
		path   string
	}{
		{"POST", "/mcp"},
		{"GET", "/oauth/authorize"},
		{"GET", "/.well-known/oauth-authorization-server"},
		{"POST", "/oauth/register"},
		{"GET", "/healthz"},
	}

	for _, tc := range passthrough {
		// Hammer each path 3 times; none should be limited.
		for i := 0; i < 3; i++ {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(tc.method, tc.path, nil)
			r.RemoteAddr = "203.0.113.40:5000"
			h.ServeHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Errorf("%s %s attempt %d: got %d, want 200 (must not be limited)",
					tc.method, tc.path, i+1, w.Code)
			}
		}
	}
}
