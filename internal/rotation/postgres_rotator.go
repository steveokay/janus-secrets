package rotation

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/steveokay/janus-secrets/internal/nethard"
)

// dbDialTimeout bounds a single DB connect attempt so a black-holed/internal IP
// cannot hang a scheduler goroutine indefinitely (finding L-2).
const dbDialTimeout = 10 * time.Second

// roleRe restricts rotatable role names to plain SQL identifiers. Combined with
// Identifier.Sanitize below it removes any injection surface from the role.
var roleRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)

// quoteLiteral renders s as a Postgres string literal, doubling single quotes.
// The generated value is alphanumeric (no quotes), so this is defensive.
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// postgresRotator resets a single role's password via ALTER ROLE.
type postgresRotator struct{ policy *nethard.Source }

func (pr postgresRotator) apply(ctx context.Context, cfg PolicyConfig, policyID, secretKey, newValue string) error {
	if cfg.AdminDSN == "" || !roleRe.MatchString(cfg.Role) {
		return ErrInvalidConfig
	}
	// Parse the DSN so we can install a bounded, SSRF-guarded dialer. pgx
	// supports Config.DialFunc; SafeDialContext blocks link-local/IMDS at
	// connect time and bounds the attempt so an unreachable internal host cannot
	// hang the scheduler goroutine.
	connCfg, err := pgx.ParseConfig(cfg.AdminDSN)
	if err != nil {
		return ErrInvalidConfig
	}
	connCfg.ConnectTimeout = dbDialTimeout
	connCfg.DialFunc = nethard.SafeDialContext(pr.policy, dbDialTimeout)

	dialCtx, cancel := context.WithTimeout(ctx, dbDialTimeout)
	defer cancel()
	conn, err := pgx.ConnectConfig(dialCtx, connCfg)
	if err != nil {
		// never surface the DSN; pgx connect errors can include host/port.
		return fmt.Errorf("%w: admin connect failed", ErrApplyFailed)
	}
	defer conn.Close(ctx)

	// ALTER ROLE cannot bind the role identifier or password as parameters;
	// both are rendered safely (Sanitize double-quotes the identifier;
	// quoteLiteral escapes the literal). Value is alphanumeric by construction.
	stmt := fmt.Sprintf("ALTER ROLE %s WITH PASSWORD %s",
		pgx.Identifier{cfg.Role}.Sanitize(), quoteLiteral(newValue))
	if _, err := conn.Exec(ctx, stmt); err != nil {
		return fmt.Errorf("%w: alter role failed", ErrApplyFailed)
	}
	return nil
}
