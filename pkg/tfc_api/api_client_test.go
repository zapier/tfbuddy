package tfc_api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// TestRateLimitedTransport_BlocksAtLimit fires concurrent requests through the
// limiter; without it the multi-workspace fan-out would 429 on TFC.
func TestRateLimitedTransport_BlocksAtLimit(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &http.Client{
		Transport: &rateLimitedTransport{
			rt:      http.DefaultTransport,
			limiter: rate.NewLimiter(rate.Limit(5), 1),
		},
		Timeout: 5 * time.Second,
	}

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

	if got := atomic.LoadInt32(&hits); got != total {
		t.Fatalf("expected %d hits, got %d", total, got)
	}
	// At 5 req/s with burst=1, 6 concurrent requests must take ~1s; an
	// unlimited client would finish almost instantly.
	if elapsed < 700*time.Millisecond {
		t.Fatalf("expected concurrent rate-limited calls to take >=700ms, got %s", elapsed)
	}
}

// TestRateLimitedTransport_HonorsContextCancel ensures a caller can abandon a
// request blocked on the limiter, so a cancelled batch unblocks promptly.
func TestRateLimitedTransport_HonorsContextCancel(t *testing.T) {
	limiter := rate.NewLimiter(rate.Limit(0.1), 1)
	if err := limiter.Wait(context.Background()); err != nil {
		t.Fatalf("priming the bucket failed: %v", err)
	}
	transport := &rateLimitedTransport{rt: http.DefaultTransport, limiter: limiter}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.invalid", nil)

	start := time.Now()
	if _, err := transport.RoundTrip(req); err == nil {
		t.Fatal("expected context-cancelled error, got nil")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("RoundTrip should have returned promptly after context cancel, took %s", elapsed)
	}
}

func TestEnvIntOrDefault(t *testing.T) {
	const key = "TFBUDDY_TEST_RATE_LIMIT"
	cases := []struct {
		name     string
		envVal   string
		fallback int
		want     int
	}{
		{"unset returns fallback", "", 30, 30},
		{"valid value parsed", "42", 30, 42},
		{"zero falls back", "0", 30, 30},
		{"negative falls back", "-7", 30, 30},
		{"garbage falls back", "not-a-number", 30, 30},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(key, tc.envVal)
			if got := envIntOrDefault(key, tc.fallback); got != tc.want {
				t.Fatalf("envIntOrDefault(%q)=%d, want %d", tc.envVal, got, tc.want)
			}
		})
	}
}
