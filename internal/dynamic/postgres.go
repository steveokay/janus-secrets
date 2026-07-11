package dynamic

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// interpolate substitutes {{name}}/{{password}}/{{expiration}}. The username and
// password are generated from a quote-free alphabet (generate.go) and expiration
// is RFC3339, so raw substitution inside the admin-authored quotes is
// injection-safe; the username is re-validated against identRe defensively.
//
// Note: unlike the rotation engine, we deliberately do NOT wrap the password with
// quoteLiteral. In this Vault-style template model the admin owns the quoting
// (their template writes PASSWORD '{{password}}'), so adding our own quotes would
// double-quote and corrupt the literal. Safety instead rests on the generator's
// quote/backslash-free alphabet — the contract enforced in generate.go.
func interpolate(tmpl, username, password string, expiration time.Time) (string, error) {
	if !identRe.MatchString(username) {
		return "", ErrInvalidConfig
	}
	r := strings.NewReplacer(
		"{{name}}", username,
		"{{password}}", password,
		"{{expiration}}", expiration.UTC().Format(time.RFC3339),
	)
	return r.Replace(tmpl), nil
}

// runStatements connects as admin and executes the (possibly multi-statement)
// SQL text. With no query arguments, pgx uses the simple protocol, which permits
// multiple semicolon-separated statements in one call. The admin DSN is never
// surfaced in returned errors.
func runStatements(ctx context.Context, adminDSN, sql string) error {
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		return fmt.Errorf("%w: admin connect failed", ErrApplyFailed)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, sql); err != nil {
		return fmt.Errorf("%w: statement exec failed", ErrApplyFailed)
	}
	return nil
}
