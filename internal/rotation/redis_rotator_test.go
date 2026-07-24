package rotation

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRESPServer accepts one connection, reads RESP command arrays, records the
// decoded commands, and replies with canned lines (default "+OK"). It lets the
// Redis rotator run its full AUTH + ACL SETUSER flow with no live Redis.
type fakeRESPServer struct {
	ln       net.Listener
	mu       sync.Mutex
	commands [][]string
	replies  []string // per-command reply lines; missing → "+OK\r\n"
}

func newFakeRESPServer(t *testing.T, replies ...string) *fakeRESPServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeRESPServer{ln: ln, replies: replies}
	go s.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *fakeRESPServer) addr() string { return s.ln.Addr().String() }

func (s *fakeRESPServer) serve() {
	conn, err := s.ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	br := bufio.NewReader(conn)
	i := 0
	for {
		cmd, err := readRESPCommand(br)
		if err != nil {
			return
		}
		s.mu.Lock()
		s.commands = append(s.commands, cmd)
		reply := "+OK\r\n"
		if i < len(s.replies) {
			reply = s.replies[i]
		}
		s.mu.Unlock()
		if _, err := conn.Write([]byte(reply)); err != nil {
			return
		}
		i++
	}
}

func (s *fakeRESPServer) got() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]string, len(s.commands))
	copy(out, s.commands)
	return out
}

// readRESPCommand decodes one RESP array-of-bulk-strings request.
func readRESPCommand(br *bufio.Reader) ([]string, error) {
	line, err := br.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if len(line) == 0 || line[0] != '*' {
		return nil, errors.New("not an array")
	}
	n, err := strconv.Atoi(strings.TrimRight(line[1:], "\r\n"))
	if err != nil {
		return nil, err
	}
	args := make([]string, 0, n)
	for j := 0; j < n; j++ {
		hdr, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if len(hdr) == 0 || hdr[0] != '$' {
			return nil, errors.New("not a bulk string")
		}
		sz, err := strconv.Atoi(strings.TrimRight(hdr[1:], "\r\n"))
		if err != nil {
			return nil, err
		}
		buf := make([]byte, sz+2)
		if _, err := io.ReadFull(br, buf); err != nil {
			return nil, err
		}
		args = append(args, string(buf[:sz]))
	}
	return args, nil
}

func testDialer(_ context.Context, addr string, _ bool, _ bool) (net.Conn, error) {
	return net.DialTimeout("tcp", addr, 2*time.Second)
}

func TestRedisRotatorAuthsAndSetsUser(t *testing.T) {
	srv := newFakeRESPServer(t) // all +OK
	rot := redisRotator{dial: testDialer}
	cfg := PolicyConfig{
		RedisAddr: srv.addr(), RedisAdminUser: "admin", RedisAdminPassword: "adminpw",
		RedisUser: "app_reader", RedisRules: "~app:* +@read",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rot.apply(ctx, cfg, "pol", "REDIS_PW", "newSecret123"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	cmds := srv.got()
	if len(cmds) != 2 {
		t.Fatalf("want AUTH + ACL SETUSER, got %d commands: %v", len(cmds), cmds)
	}
	// AUTH admin adminpw
	if len(cmds[0]) != 3 || cmds[0][0] != "AUTH" || cmds[0][1] != "admin" || cmds[0][2] != "adminpw" {
		t.Fatalf("bad AUTH command: %v", cmds[0])
	}
	// ACL SETUSER app_reader reset on >newSecret123 ~app:* +@read
	want := []string{"ACL", "SETUSER", "app_reader", "reset", "on", ">newSecret123", "~app:*", "+@read"}
	got := cmds[1]
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("SETUSER = %v, want %v", got, want)
	}
}

func TestRedisRotatorSingleArgAuthForRequirepass(t *testing.T) {
	srv := newFakeRESPServer(t)
	rot := redisRotator{dial: testDialer}
	cfg := PolicyConfig{RedisAddr: srv.addr(), RedisAdminPassword: "onlypw", RedisUser: "u"}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rot.apply(ctx, cfg, "p", "K", "v12345"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	cmds := srv.got()
	if len(cmds[0]) != 2 || cmds[0][0] != "AUTH" || cmds[0][1] != "onlypw" {
		t.Fatalf("want single-arg AUTH, got %v", cmds[0])
	}
}

func TestRedisRotatorErrorReplyIsSanitized(t *testing.T) {
	// AUTH ok, ACL SETUSER rejected with a detailed error.
	srv := newFakeRESPServer(t, "+OK\r\n", "-ERR unknown user 'app_reader' with password topSecretValue\r\n")
	rot := redisRotator{dial: testDialer}
	cfg := PolicyConfig{RedisAddr: srv.addr(), RedisAdminPassword: "adminpw", RedisUser: "app_reader"}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := rot.apply(ctx, cfg, "p", "K", "topSecretValue")
	if !errors.Is(err, ErrApplyFailed) {
		t.Fatalf("want ErrApplyFailed, got %v", err)
	}
	for _, leak := range []string{"topSecretValue", "adminpw", "unknown user"} {
		if strings.Contains(err.Error(), leak) {
			t.Fatalf("error leaked %q: %v", leak, err)
		}
	}
}

func TestRedisRotatorRejectsUnsafeUserAndRules(t *testing.T) {
	rot := redisRotator{dial: testDialer}
	// bad username
	if err := rot.apply(context.Background(), PolicyConfig{RedisAddr: "x:6379", RedisUser: "bad user"}, "p", "K", "v"); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("want ErrInvalidConfig for bad user, got %v", err)
	}
	// rule attempting to inject a password / nopass
	for _, rule := range []string{">injected", "nopass", "resetpass", "<oldpw", "bad'quote", "semi;colon"} {
		cfg := PolicyConfig{RedisAddr: "x:6379", RedisUser: "ok", RedisRules: "~a:* " + rule}
		if err := rot.apply(context.Background(), cfg, "p", "K", "v"); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("rule %q: want ErrInvalidConfig, got %v", rule, err)
		}
	}
}

func TestRedisRotatorNoAuthWhenOpen(t *testing.T) {
	srv := newFakeRESPServer(t)
	rot := redisRotator{dial: testDialer}
	cfg := PolicyConfig{RedisAddr: srv.addr(), RedisUser: "u"} // no admin creds
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rot.apply(ctx, cfg, "p", "K", "v12345"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	cmds := srv.got()
	if len(cmds) != 1 || cmds[0][0] != "ACL" {
		t.Fatalf("want only ACL SETUSER (no AUTH), got %v", cmds)
	}
}
