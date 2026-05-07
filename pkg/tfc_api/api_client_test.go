package tfc_api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/viper"
	"golang.org/x/time/rate"
)

// TestRateLimitedTransport_Sequential verifies the limiter throttles back-to-back
// requests on the same client.
func TestRateLimitedTransport_Sequential(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	limiter := rate.NewLimiter(rate.Limit(5), 1)
	client := newRateLimitedHTTPClient(limiter)

	const total = 6
	start := time.Now()
	for i := 0; i < total; i++ {
		req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		resp.Body.Close()
	}
	elapsed := time.Since(start)

	// At 5 req/s, burst=1: first call immediate, remaining 5 wait ~200ms each.
	if elapsed < 700*time.Millisecond {
		t.Fatalf("expected rate-limited calls to take >=700ms, got %s", elapsed)
	}
}

// TestRateLimitedTransport_ConcurrentSharing verifies multiple goroutines share
// the same limiter, which is the production scenario when several workspaces
// fan out concurrently.
func TestRateLimitedTransport_ConcurrentSharing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	limiter := rate.NewLimiter(rate.Limit(5), 1)
	client := newRateLimitedHTTPClient(limiter)

	const total = 6
	var wg sync.WaitGroup
	wg.Add(total)
	start := time.Now()
	for i := 0; i < total; i++ {
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
			resp, err := client.Do(req)
			if err != nil {
				t.Errorf("request failed: %v", err)
				return
			}
			resp.Body.Close()
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	// All 6 callers compete for the same bucket: even when fully parallel they
	// must wait ~200ms per token after the initial burst.
	if elapsed < 700*time.Millisecond {
		t.Fatalf("expected concurrent callers to coordinate via the shared limiter (>=700ms), got %s", elapsed)
	}
}

// TestRateLimitedTransport_HonorsContextCancel ensures callers can abandon a
// request blocked on the limiter, e.g. when JetStream cancels processing.
func TestRateLimitedTransport_HonorsContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	limiter := rate.NewLimiter(rate.Limit(0.1), 1)
	transport := &rateLimitedTransport{rt: http.DefaultTransport, limiter: limiter}

	if err := limiter.Wait(context.Background()); err != nil {
		t.Fatalf("priming bucket failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)

	start := time.Now()
	_, err := transport.RoundTrip(req)
	if err == nil {
		t.Fatal("expected context-cancelled error, got nil")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("RoundTrip should have returned promptly after cancel, took %s", elapsed)
	}
}

// TestNewRateLimitedHTTPClient_NoHardTimeout guards against re-introducing
// http.Client.Timeout. ConfigurationVersions.Upload streams the cloned repo
// so a fixed top-level timeout would truncate slow uploads on large repos.
func TestNewRateLimitedHTTPClient_NoHardTimeout(t *testing.T) {
	client := newRateLimitedHTTPClient(rate.NewLimiter(rate.Limit(10), 1))
	if client.Timeout != 0 {
		t.Fatalf("expected zero http.Client.Timeout, got %s", client.Timeout)
	}
}

// TestTFCRateLimitValue verifies viper-driven defaults and fallback behavior.
func TestTFCRateLimitValue(t *testing.T) {
	const key = "TEST_RATE_LIMIT"
	t.Cleanup(func() { viper.Reset() })

	cases := []struct {
		name     string
		setEnv   string
		fallback int
		want     int
	}{
		{"unset returns fallback", "", 30, 30},
		{"valid value parsed", "42", 30, 42},
		{"zero falls back", "0", 30, 30},
		{"negative falls back", "-7", 30, 30},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			viper.Reset()
			viper.SetEnvPrefix("TFBUDDY")
			viper.AutomaticEnv()
			if tc.setEnv != "" {
				t.Setenv("TFBUDDY_"+key, tc.setEnv)
			}
			got := tfcRateLimitValue(key, tc.fallback)
			if got != tc.want {
				t.Fatalf("tfcRateLimitValue(%q)=%d, want %d", tc.setEnv, got, tc.want)
			}
		})
	}
}

// TestRateLimitedTransport_RoundTripIsConcurrencySafe asserts the transport
// can be hit from many goroutines without panics or data races (run with -race).
func TestRateLimitedTransport_RoundTripIsConcurrencySafe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	transport := &rateLimitedTransport{
		rt:      http.DefaultTransport,
		limiter: rate.NewLimiter(rate.Inf, 1),
	}

	var wg sync.WaitGroup
	const goroutines = 20
	var done int32
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
			resp, err := transport.RoundTrip(req)
			if err != nil {
				t.Errorf("RoundTrip error: %v", err)
				return
			}
			resp.Body.Close()
			atomic.AddInt32(&done, 1)
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt32(&done); got != goroutines {
		t.Fatalf("expected %d successful round trips, got %d", goroutines, got)
	}
}
