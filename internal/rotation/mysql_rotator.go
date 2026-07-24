package rotation

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

// mysqlUserRe restricts the rotatable MySQL account name to a strict identifier
// charset. MySQL account names cannot be bound as query parameters, so the only
// safe path is to validate against a conservative allowlist and then quote. We
// allow the common `user_name`, `service.account`, and digits; no quotes,
// backslashes, backticks, or whitespace can appear.
var mysqlUserRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// mysqlHostRe restricts the account host part ('user'@'host'). Hostnames,
// wildcards (`%`), IPv4 literals, and netmask forms are permitted; quotes,
// backslashes, and whitespace are not. Empty host defaults to '%'.
var mysqlHostRe = regexp.MustCompile(`^[A-Za-z0-9_.%:/-]{1,255}$`)

// quoteMySQLString renders s as a MySQL single-quoted string literal, escaping
// the characters MySQL treats specially inside quotes. Used only for the
// account identifier's user/host parts (which are validated first); the new
// password is never string-interpolated — it is always a bound parameter.
func quoteMySQLString(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`'`, `\'`,
		"\x00", `\0`,
		"\n", `\n`,
		"\r", `\r`,
		"\x1a", `\Z`,
	)
	return "'" + r.Replace(s) + "'"
}

// mysqlAccount builds the safely-quoted `'user'@'host'` account spec from the
// validated config, or returns ErrInvalidConfig if either part fails the strict
// charset check. Host defaults to '%' when unset.
func mysqlAccount(user, host string) (string, error) {
	if !mysqlUserRe.MatchString(user) {
		return "", ErrInvalidConfig
	}
	if host == "" {
		host = "%"
	}
	if !mysqlHostRe.MatchString(host) {
		return "", ErrInvalidConfig
	}
	return quoteMySQLString(user) + "@" + quoteMySQLString(host), nil
}

// alterUserStmt returns the ALTER USER statement with the account rendered
// inline (validated + quoted) and a `?` placeholder for the new password, which
// the caller binds as a parameter — MySQL 5.7.6+/8.0 accept a bound password in
// `ALTER USER ... IDENTIFIED BY ?`. The password is never interpolated.
func alterUserStmt(account string) string {
	return "ALTER USER " + account + " IDENTIFIED BY ?"
}

// sqlOpener is a seam over sql.Open so tests can register a fake driver and
// exercise query-building/error-sanitization without a live MySQL. Production
// uses database/sql with the mysql driver name.
var sqlOpen = sql.Open

// mysqlRotator resets a single MySQL account's password via ALTER USER.
type mysqlRotator struct{}

func (mysqlRotator) apply(ctx context.Context, cfg PolicyConfig, policyID, secretKey, newValue string) error {
	dsn, err := mysqlDSN(cfg)
	if err != nil {
		return err
	}
	account, err := mysqlAccount(cfg.MySQLUser, cfg.MySQLHost)
	if err != nil {
		return err
	}

	db, err := sqlOpen("mysql", dsn)
	if err != nil {
		// never surface the DSN (it carries the admin password/host).
		return fmt.Errorf("%w: admin connect failed", ErrApplyFailed)
	}
	defer db.Close()

	pingCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		return fmt.Errorf("%w: admin connect failed", ErrApplyFailed)
	}

	// The password is bound as a parameter, so no escaping of the value is
	// needed; the account identifier is validated + quoted above.
	if _, err := db.ExecContext(ctx, alterUserStmt(account), newValue); err != nil {
		return fmt.Errorf("%w: alter user failed", ErrApplyFailed)
	}
	return nil
}

// mysqlDSN builds the go-sql-driver DSN from either an explicit AdminDSN or the
// discrete host/port/admin-user/admin-password fields. TLS is honoured: a DSN
// may carry `?tls=...`; the discrete form sets `tls` from MySQLTLS. The DSN is
// treated as a secret and never logged.
func mysqlDSN(cfg PolicyConfig) (string, error) {
	if cfg.MySQLUser == "" {
		return "", ErrInvalidConfig
	}
	if cfg.AdminDSN != "" {
		// Validate it parses as a mysql DSN so a malformed value fails fast
		// as invalid config rather than a runtime connect error. The parsed
		// value is discarded; the original string is passed to the driver.
		if _, err := mysql.ParseDSN(cfg.AdminDSN); err != nil {
			return "", ErrInvalidConfig
		}
		return cfg.AdminDSN, nil
	}
	if cfg.MySQLAddr == "" || cfg.MySQLAdminUser == "" {
		return "", ErrInvalidConfig
	}
	c := mysql.NewConfig()
	c.Net = "tcp"
	c.Addr = cfg.MySQLAddr
	c.User = cfg.MySQLAdminUser
	c.Passwd = cfg.MySQLAdminPassword
	c.DBName = cfg.MySQLDBName
	switch strings.ToLower(cfg.MySQLTLS) {
	case "", "false", "off":
		// leave default (no TLS)
	case "true", "on", "preferred", "skip-verify":
		c.TLSConfig = strings.ToLower(cfg.MySQLTLS)
	default:
		return "", ErrInvalidConfig
	}
	return c.FormatDSN(), nil
}
