package rotation

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"sync"
	"testing"
)

// ── fake database/sql driver ────────────────────────────────────────────────
// A minimal driver that records the exec query + args and returns a canned
// error, so we can assert the built ALTER USER statement, the bound password,
// and error sanitization WITHOUT a live MySQL.

type fakeExec struct {
	query string
	args  []driver.NamedValue
}

type fakeConn struct {
	mu      *sync.Mutex
	execs   *[]fakeExec
	pingErr error
	execErr error
}

func (c fakeConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("not used") }
func (c fakeConn) Close() error                        { return nil }
func (c fakeConn) Begin() (driver.Tx, error)           { return nil, errors.New("not used") }

func (c fakeConn) Ping(context.Context) error { return c.pingErr }

func (c fakeConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.mu.Lock()
	*c.execs = append(*c.execs, fakeExec{query: query, args: args})
	c.mu.Unlock()
	if c.execErr != nil {
		return nil, c.execErr
	}
	return driver.RowsAffected(1), nil
}

type fakeDriver struct {
	mu      sync.Mutex
	execs   []fakeExec
	pingErr error
	execErr error
}

func (d *fakeDriver) Open(string) (driver.Conn, error) {
	return fakeConn{mu: &d.mu, execs: &d.execs, pingErr: d.pingErr, execErr: d.execErr}, nil
}

// registerFakeDriver installs d under a unique name and swaps sqlOpen to use it,
// restoring the original on cleanup.
func registerFakeDriver(t *testing.T, name string, d *fakeDriver) {
	t.Helper()
	sql.Register(name, d)
	orig := sqlOpen
	sqlOpen = func(_, dsn string) (*sql.DB, error) { return sql.Open(name, dsn) }
	t.Cleanup(func() { sqlOpen = orig })
}

func TestMySQLRotatorBuildsAlterUserAndBindsPassword(t *testing.T) {
	d := &fakeDriver{}
	registerFakeDriver(t, "fakemysql_ok", d)

	rot := mysqlRotator{}
	cfg := PolicyConfig{
		MySQLAddr: "db:3306", MySQLAdminUser: "rotator", MySQLAdminPassword: "adminpw",
		MySQLUser: "app_user", MySQLHost: "10.0.0.%",
	}
	const newPW = "brandNewPW123"
	if err := rot.apply(context.Background(), cfg, "pol", "DB_PASSWORD", newPW); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(d.execs) != 1 {
		t.Fatalf("want 1 exec, got %d", len(d.execs))
	}
	q := d.execs[0].query
	if !strings.HasPrefix(q, "ALTER USER '") || !strings.Contains(q, "'app_user'@'10.0.0.%'") {
		t.Fatalf("unexpected statement: %q", q)
	}
	if !strings.HasSuffix(q, "IDENTIFIED BY ?") {
		t.Fatalf("password must be a bound placeholder, got: %q", q)
	}
	// The new password must be a bound argument, never interpolated.
	if strings.Contains(q, newPW) {
		t.Fatalf("password leaked into the SQL text: %q", q)
	}
	if len(d.execs[0].args) != 1 || d.execs[0].args[0].Value != newPW {
		t.Fatalf("password not bound as arg: %+v", d.execs[0].args)
	}
}

func TestMySQLRotatorRejectsUnsafeIdentifiers(t *testing.T) {
	rot := mysqlRotator{}
	base := PolicyConfig{MySQLAddr: "db:3306", MySQLAdminUser: "rotator"}
	cases := []struct{ user, host string }{
		{"bad'; DROP", "%"},
		{"user`tick", "%"},
		{"has space", "%"},
		{"ok_user", "host' OR '1"},
		{"ok_user", "with space"},
		{"", "%"},
	}
	for _, tc := range cases {
		c := base
		c.MySQLUser, c.MySQLHost = tc.user, tc.host
		if err := rot.apply(context.Background(), c, "p", "K", "v"); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("user=%q host=%q: want ErrInvalidConfig, got %v", tc.user, tc.host, err)
		}
	}
}

func TestMySQLRotatorSanitizesExecError(t *testing.T) {
	d := &fakeDriver{execErr: errors.New("Access denied for user 'rotator'@'db' (using password: YES)")}
	registerFakeDriver(t, "fakemysql_execerr", d)

	rot := mysqlRotator{}
	cfg := PolicyConfig{MySQLAddr: "secret-host:3306", MySQLAdminUser: "rotator", MySQLAdminPassword: "supersecret", MySQLUser: "app_user"}
	err := rot.apply(context.Background(), cfg, "p", "K", "topSecretValue")
	if !errors.Is(err, ErrApplyFailed) {
		t.Fatalf("want ErrApplyFailed, got %v", err)
	}
	for _, leak := range []string{"supersecret", "topSecretValue", "secret-host", "Access denied"} {
		if strings.Contains(err.Error(), leak) {
			t.Fatalf("error leaked %q: %v", leak, err)
		}
	}
}

func TestMySQLDSNFromDiscreteFieldsHonoursTLS(t *testing.T) {
	dsn, err := mysqlDSN(PolicyConfig{
		MySQLAddr: "db:3306", MySQLAdminUser: "admin", MySQLAdminPassword: "pw",
		MySQLUser: "app", MySQLTLS: "skip-verify",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dsn, "tls=skip-verify") {
		t.Fatalf("tls option not honoured: %q", dsn)
	}
	// unknown tls mode → invalid config
	if _, err := mysqlDSN(PolicyConfig{MySQLAddr: "db:3306", MySQLAdminUser: "a", MySQLUser: "u", MySQLTLS: "bogus"}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("want ErrInvalidConfig for bad tls mode, got %v", err)
	}
	// explicit AdminDSN passes through after validating it parses.
	got, err := mysqlDSN(PolicyConfig{AdminDSN: "admin:pw@tcp(db:3306)/app?tls=true", MySQLUser: "u"})
	if err != nil || !strings.Contains(got, "tls=true") {
		t.Fatalf("AdminDSN passthrough failed: %q %v", got, err)
	}
	// malformed AdminDSN → invalid config
	if _, err := mysqlDSN(PolicyConfig{AdminDSN: "::::not a dsn", MySQLUser: "u"}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("want ErrInvalidConfig for malformed DSN, got %v", err)
	}
}
