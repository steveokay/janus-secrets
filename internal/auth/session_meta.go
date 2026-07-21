package auth

import "context"

// SessionMeta is non-secret client metadata (IP, user-agent) captured when a
// session is minted, so a user can later recognize their sessions in the
// management surface. It is threaded via context rather than added to Login /
// OIDC-callback signatures so those stay stable and HTTP-free.
type SessionMeta struct {
	IP        string
	UserAgent string
}

type sessionMetaKey struct{}

// WithSessionMeta attaches session metadata for the createSession call made
// while handling this request. Absent metadata simply persists as NULL.
func WithSessionMeta(ctx context.Context, m SessionMeta) context.Context {
	return context.WithValue(ctx, sessionMetaKey{}, m)
}

func sessionMetaFrom(ctx context.Context) SessionMeta {
	m, _ := ctx.Value(sessionMetaKey{}).(SessionMeta)
	return m
}
