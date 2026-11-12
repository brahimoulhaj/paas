package api

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"panel/internal/auth"
	"panel/internal/brand"
	"panel/internal/store"
)

type ctxKey int

const (
	userKey ctxKey = iota
	tokenKey
)

func userFrom(ctx context.Context) store.User {
	u, _ := ctx.Value(userKey).(store.User)
	return u
}

// tokenFrom is the API token the request was authenticated with; nil for a browser session.
func tokenFrom(ctx context.Context) *store.APIToken {
	t, _ := ctx.Value(tokenKey).(*store.APIToken)
	return t
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach Flush on the underlying writer (needed for SSE).
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.status = http.StatusSwitchingProtocols
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

// logRequests logs API calls; static asset hits are skipped to keep the log readable.
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/health" {
			s.log.Info("request", "method", r.Method, "path", r.URL.Path, "status", sw.status,
				"ms", time.Since(start).Milliseconds(), "ip", clientIP(r))
		}
	})
}

func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				s.log.Error("panic", "error", v, "path", r.URL.Path, "stack", string(debug.Stack()))
				writeError(w, http.StatusInternalServerError, "internal", "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// sameOrigin blocks cross-site writes to the cookie-authenticated API (CSRF).
// Browsers send Sec-Fetch-Site and Origin on such requests; non-browser clients send
// neither and are let through, since they cannot carry a victim's cookie.
// Token-authenticated webhooks under /api/hooks/ are exempt.
func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		safe := r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions
		if safe || !strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/api/hooks/") {
			next.ServeHTTP(w, r)
			return
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
			writeError(w, http.StatusForbidden, "cross_origin", "cross-origin request blocked")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Host != r.Host {
				writeError(w, http.StatusForbidden, "cross_origin", "cross-origin request blocked")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// requireAuth resolves the request to a user or answers 401. An "Authorization: Bearer"
// header is an API token and takes precedence; otherwise the session cookie is used.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h := r.Header.Get("Authorization"); h != "" {
			s.tokenAuth(w, r, h, next)
			return
		}
		c, err := r.Cookie(brand.SessionCookie())
		if err != nil || c.Value == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "sign in to continue")
			return
		}
		u, err := s.accounts.SessionUser(r.Context(), c.Value)
		if err != nil {
			if err != store.ErrNotFound {
				s.log.Error("session lookup", "error", err)
			}
			writeError(w, http.StatusUnauthorized, "unauthorized", "sign in to continue")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), userKey, u)))
	}
}

func (s *Server) tokenAuth(w http.ResponseWriter, r *http.Request, header string, next http.HandlerFunc) {
	scheme, raw, ok := strings.Cut(header, " ")
	raw = strings.TrimSpace(raw)
	if !ok || !strings.EqualFold(scheme, "Bearer") || raw == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized", "the Authorization header must be \"Bearer <token>\"")
		return
	}
	t, u, err := s.store.APITokenUser(r.Context(), auth.HashToken(raw))
	if err != nil {
		if err != store.ErrNotFound {
			s.log.Error("api token lookup", "error", err)
		}
		writeError(w, http.StatusUnauthorized, "unauthorized", "invalid or revoked API token")
		return
	}
	if err := s.store.TouchAPIToken(r.Context(), t.ID); err != nil {
		s.log.Error("api token touch", "error", err)
	}
	ctx := context.WithValue(context.WithValue(r.Context(), userKey, u), tokenKey, &t)
	next(w, r.WithContext(ctx))
}

// sessionOnly refuses API tokens: managing tokens needs the browser session, so a leaked
// token cannot mint more of itself or hide its tracks by revoking others.
func sessionOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if tokenFrom(r.Context()) != nil {
			writeError(w, http.StatusForbidden, "session_required", "this endpoint needs a signed-in browser session, not an API token")
			return
		}
		next(w, r)
	}
}

// clientIP is the address of the browser. Behind the proxy every request arrives from
// Caddy, which sets X-Forwarded-For to the real client and discards whatever the client
// sent. The header is believed only when the direct peer is on a private or loopback
// address, so a client that reaches the panel port directly cannot pick its own address.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil || !(peer.IsPrivate() || peer.IsLoopback()) {
		return host
	}
	xff := r.Header.Get("X-Forwarded-For")
	if i := strings.LastIndexByte(xff, ','); i >= 0 {
		xff = xff[i+1:]
	}
	if fwd, err := netip.ParseAddr(strings.TrimSpace(xff)); err == nil {
		return fwd.String()
	}
	return host
}

// rateLimiter is a fixed-window counter per key, held in memory.
type rateLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string]*bucket
}

type bucket struct {
	count int
	reset time.Time
}

func newRateLimiter(max int, window time.Duration) *rateLimiter {
	return &rateLimiter{max: max, window: window, hits: map[string]*bucket{}}
}

// allow records one attempt and reports whether it is within the limit.
func (l *rateLimiter) allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if len(l.hits) > 10_000 { // bound memory under a flood of distinct keys
		for k, b := range l.hits {
			if now.After(b.reset) {
				delete(l.hits, k)
			}
		}
	}
	b := l.hits[key]
	if b == nil || now.After(b.reset) {
		b = &bucket{reset: now.Add(l.window)}
		l.hits[key] = b
	}
	b.count++
	return b.count <= l.max, b.reset.Sub(now)
}

func (s *Server) limit(l *rateLimiter, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ok, retry := l.allow(clientIP(r))
		if !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts, try again later")
			return
		}
		next(w, r)
	}
}
