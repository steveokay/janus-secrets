package api

import (
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// ipRateLimiter is a per-client-IP token bucket. Right-sized for the
// supported single-node topology; distributed limiting is a non-goal.
type ipRateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*ipBucket
	rate     rate.Limit
	burst    int
	lastScan time.Time
}

type ipBucket struct {
	lim      *rate.Limiter
	lastSeen time.Time
}

// newIPRateLimiter allows r sustained requests/second with the given burst,
// per client IP.
func newIPRateLimiter(r float64, burst int) *ipRateLimiter {
	return &ipRateLimiter{
		buckets:  make(map[string]*ipBucket),
		rate:     rate.Limit(r),
		burst:    burst,
		lastScan: time.Now(),
	}
}

func (l *ipRateLimiter) allow(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	// Coarse pruning bounds memory: drop buckets idle for over an hour.
	if time.Since(l.lastScan) > 10*time.Minute {
		for ip, b := range l.buckets {
			if time.Since(b.lastSeen) > time.Hour {
				delete(l.buckets, ip)
			}
		}
		l.lastScan = time.Now()
	}
	b, ok := l.buckets[host]
	if !ok {
		b = &ipBucket{lim: rate.NewLimiter(l.rate, l.burst)}
		l.buckets[host] = b
	}
	b.lastSeen = time.Now()
	return b.lim.Allow()
}

// middleware rejects over-limit requests with 429 rate_limited.
func (l *ipRateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.allow(r.RemoteAddr) {
			writeError(w, http.StatusTooManyRequests, CodeRateLimited,
				"too many attempts; retry later")
			return
		}
		next.ServeHTTP(w, r)
	})
}
