package rotation

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// redisUserRe restricts the rotatable Redis ACL username to a strict charset.
// Redis ACL usernames are space-delimited tokens in the wire protocol; we only
// permit a conservative identifier so the username can never inject an extra
// ACL argument. `default` and typical `svc-name`/`app.reader` forms pass.
var redisUserRe = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)

// redisRuleRe restricts each optional preserved ACL rule token (keys/commands/
// categories/channels, e.g. `~app:*`, `+@read`, `-flushdb`, `allkeys`). It is a
// conservative allowlist of the characters Redis ACL rule tokens use; anything
// outside it (including whitespace, which would split into extra args) is
// rejected so a rule can never smuggle a `>password` or `nopass` token.
var redisRuleRe = regexp.MustCompile(`^[A-Za-z0-9_.:*&~@+\-|]{1,256}$`)

// redisDialer is a seam over the TCP/TLS dial so tests can point the rotator at
// a fake RESP server via net.Listen without live Redis or real TLS.
type redisDialer func(ctx context.Context, addr string, useTLS bool, skipVerify bool) (net.Conn, error)

func defaultRedisDial(ctx context.Context, addr string, useTLS bool, skipVerify bool) (net.Conn, error) {
	d := net.Dialer{Timeout: 15 * time.Second}
	if !useTLS {
		return d.DialContext(ctx, "tcp", addr)
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	// #nosec G402 -- InsecureSkipVerify is an explicit, per-policy opt-in
	// (documented footgun) for self-hosted Redis with private/self-signed CAs;
	// it is off unless the operator sets redis_skip_verify on the policy.
	cfg := &tls.Config{ServerName: host, InsecureSkipVerify: skipVerify}
	td := tls.Dialer{NetDialer: &d, Config: cfg}
	return td.DialContext(ctx, "tcp", addr)
}

// redisRotator resets a single Redis ACL user's password via ACL SETUSER.
type redisRotator struct {
	dial redisDialer
}

func (rr redisRotator) apply(ctx context.Context, cfg PolicyConfig, policyID, secretKey, newValue string) error {
	if cfg.RedisAddr == "" || !redisUserRe.MatchString(cfg.RedisUser) {
		return ErrInvalidConfig
	}
	rules, err := redisRules(cfg.RedisRules)
	if err != nil {
		return err
	}

	dial := rr.dial
	if dial == nil {
		dial = defaultRedisDial
	}
	conn, err := dial(ctx, cfg.RedisAddr, cfg.RedisTLS, cfg.RedisSkipVerify)
	if err != nil {
		// never surface the address/credentials.
		return fmt.Errorf("%w: admin connect failed", ErrApplyFailed)
	}
	defer conn.Close()

	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	} else {
		_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	}

	br := bufio.NewReader(conn)

	// AUTH first. Redis 6+ supports `AUTH <user> <pass>`; classic
	// requirepass uses `AUTH <pass>`. When an admin user is set we send the
	// two-arg form, else the single-arg form. AUTH is skipped only if neither
	// an admin user nor password is configured (open instance).
	if cfg.RedisAdminUser != "" || cfg.RedisAdminPassword != "" {
		var authArgs []string
		if cfg.RedisAdminUser != "" {
			authArgs = []string{"AUTH", cfg.RedisAdminUser, cfg.RedisAdminPassword}
		} else {
			authArgs = []string{"AUTH", cfg.RedisAdminPassword}
		}
		if err := redisCommand(conn, br, authArgs); err != nil {
			return fmt.Errorf("%w: auth failed", ErrApplyFailed)
		}
	}

	// ACL SETUSER <user> reset on >NEWPASS <rules...>
	//   reset  — clear existing passwords/rules for a deterministic result
	//   on     — enable the user
	//   >PASS  — add the new password (Redis hashes it; the cleartext travels
	//            only over this connection, never logged)
	// Preserved rules (if any) re-grant the user's key/command/channel perms.
	setArgs := []string{"ACL", "SETUSER", cfg.RedisUser, "reset", "on", ">" + newValue}
	setArgs = append(setArgs, rules...)
	if err := redisCommand(conn, br, setArgs); err != nil {
		return fmt.Errorf("%w: acl setuser failed", ErrApplyFailed)
	}
	return nil
}

// redisRules validates and returns the optional space-separated preserved ACL
// rule tokens. An empty config yields a documented minimal ruleset that leaves
// the user enabled with the new password but no key/command grants (operators
// who need to preserve access supply their rules explicitly).
func redisRules(raw string) ([]string, error) {
	fields := strings.Fields(raw)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		// Never allow a rule to (re)set a password or clear auth requirements.
		if strings.HasPrefix(f, ">") || strings.HasPrefix(f, "<") ||
			strings.HasPrefix(f, "#") || strings.EqualFold(f, "nopass") ||
			strings.EqualFold(f, "resetpass") {
			return nil, ErrInvalidConfig
		}
		if !redisRuleRe.MatchString(f) {
			return nil, ErrInvalidConfig
		}
		out = append(out, f)
	}
	return out, nil
}

// redisCommand encodes args as a RESP array of bulk strings, writes it, and
// reads exactly one reply, returning an error for a RESP error reply (`-...`)
// or a malformed/partial response. The command bytes (which include the new
// password / admin password) are never logged; the returned error carries only
// a fixed category.
func redisCommand(conn net.Conn, br *bufio.Reader, args []string) error {
	if _, err := conn.Write(encodeRESP(args)); err != nil {
		return err
	}
	return readRESPReply(br)
}

// encodeRESP encodes args as a RESP array of bulk strings.
func encodeRESP(args []string) []byte {
	var b strings.Builder
	b.WriteString("*")
	b.WriteString(strconv.Itoa(len(args)))
	b.WriteString("\r\n")
	for _, a := range args {
		b.WriteString("$")
		b.WriteString(strconv.Itoa(len(a)))
		b.WriteString("\r\n")
		b.WriteString(a)
		b.WriteString("\r\n")
	}
	return []byte(b.String())
}

// readRESPReply reads a single top-level RESP reply and returns an error if it
// is a RESP error (`-ERR ...`). It handles the reply types Redis returns for
// AUTH / ACL SETUSER: simple string (+), error (-), integer (:), and bulk
// string ($, including null). It does not surface reply contents in errors.
func readRESPReply(br *bufio.Reader) error {
	line, err := br.ReadString('\n')
	if err != nil {
		return err
	}
	if len(line) < 3 || !strings.HasSuffix(line, "\r\n") {
		return fmt.Errorf("%w: malformed reply", ErrApplyFailed)
	}
	switch line[0] {
	case '+', ':':
		return nil
	case '-':
		return fmt.Errorf("%w: command rejected", ErrApplyFailed)
	case '$':
		n, err := strconv.Atoi(strings.TrimRight(line[1:], "\r\n"))
		if err != nil {
			return fmt.Errorf("%w: malformed reply", ErrApplyFailed)
		}
		if n < 0 {
			return nil // null bulk string
		}
		buf := make([]byte, n+2) // payload + CRLF
		if _, err := readFull(br, buf); err != nil {
			return err
		}
		return nil
	default:
		return fmt.Errorf("%w: unexpected reply", ErrApplyFailed)
	}
}

// readFull reads len(buf) bytes from br.
func readFull(br *bufio.Reader, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := br.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
