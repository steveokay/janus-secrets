package rotation

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
)

// roleRe restricts rotatable role names to plain SQL identifiers. Combined with
// Identifier.Sanitize below it removes any injection surface from the role.
var roleRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)

// quoteLiteral renders s as a Postgres string literal, doubling single quotes.
// The generated value is alphanumeric (no quotes), so this is defensive.
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// postgresRotator resets a single role's password via ALTER ROLE.
type postgresRotator struct{}

func (postgresRotator) apply(ctx context.Context, cfg PolicyConfig, policyID, secretKey, newValue string) error {
	if cfg.AdminDSN == "" || !roleRe.MatchString(cfg.Role) {
		return ErrInvalidConfig
	}
	conn, err := pgx.Connect(ctx, cfg.AdminDSN)
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
