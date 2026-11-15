package middleware

import (
	"context"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultLoginRate allows 10 tokens per minute refilled continuously.
	DefaultLoginRate       = 10.0 / 60.0
	DefaultLoginBurst      = 10
	DefaultRateLimitStale  = 10 * time.Minute
	defaultCleanupInterval = 2 * time.Minute
)

type tokenBucket struct {
	tokens   float64
	lastSeen time.Time
}

// IPRateLimiter enforces per-IP token-bucket rate limiting with background eviction of stale entries.
type IPRateLimiter struct {
	mu         sync.Mutex
	buckets    map[string]*tokenBucket
	rate       float64
	burst      float64
	staleAfter time.Duration
	now        func() time.Time
}

// NewIPRateLimiter creates a per-IP token-bucket limiter and starts stale-bucket cleanup tied to ctx.
func NewIPRateLimiter(ctx context.Context, ratePerSec float64, burst int, staleAfter time.Duration) *IPRateLimiter {
	if ratePerSec <= 0 {
		ratePerSec = DefaultLoginRate
	}
	if burst <= 0 {
		burst = DefaultLoginBurst
	}
	if staleAfter <= 0 {
		staleAfter = DefaultRateLimitStale
	}

	l := &IPRateLimiter{
		buckets:    make(map[string]*tokenBucket),
		rate:       ratePerSec,
		burst:      float64(burst),
		staleAfter: staleAfter,
		now:        time.Now,
	}

	cleanupEvery := staleAfter / 2
	if cleanupEvery <= 0 {
		cleanupEvery = defaultCleanupInterval
	}
	l.startCleanup(ctx, cleanupEvery)
	return l
}

// Allow consumes 1 token for key and reports whether the request is allowed along with wait duration if denied.
func (l *IPRateLimiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	b, ok := l.buckets[key]
	if !ok {
		l.buckets[key] = &tokenBucket{
			tokens:   l.burst - 1,
			lastSeen: now,
		}
		return true, 0
	}

	elapsed := now.Sub(b.lastSeen).Seconds()
	if elapsed > 0 {
		b.tokens = math.Min(l.burst, b.tokens+elapsed*l.rate)
	}
	b.lastSeen = now

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}

	needed := (1 - b.tokens) / l.rate
	wait := time.Duration(math.Ceil(needed)) * time.Second
	if wait < time.Second {
		wait = time.Second
	}
	return false, wait
}

// Middleware rejects excess requests per IP with HTTP 429 Too Many Requests.
func (l *IPRateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := extractClientIP(r)
		allowed, retryAfter := l.Allow(ip)
		if !allowed {
			secs := max(1, int(math.Ceil(retryAfter.Seconds())))
			w.Header().Set("Retry-After", strconv.Itoa(secs))
			http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (l *IPRateLimiter) startCleanup(ctx context.Context, interval time.Duration) {
	if ctx == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				l.evictStale()
			}
		}
	}()
}

func (l *IPRateLimiter) evictStale() {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := l.now().Add(-l.staleAfter)
	for ip, b := range l.buckets {
		if b.lastSeen.Before(cutoff) {
			delete(l.buckets, ip)
		}
	}
}

func extractClientIP(r *http.Request) string {
	addr := strings.TrimSpace(r.RemoteAddr)
	if host, _, err := net.SplitHostPort(addr); err == nil && host != "" {
		return host
	}
	if addr != "" {
		return addr
	}
	return "unknown"
}
