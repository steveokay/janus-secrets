package rotation

import (
	"bufio"
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// parseRESPArray is a strict RESP array reader used only by the fuzz test to
// prove what encodeRESP actually emits. It deliberately re-implements the wire
// format (rather than reusing production code) so a bug in the encoder cannot
// be masked by a matching bug in the decoder.
func parseRESPArray(b []byte) ([]string, error) {
	br := bufio.NewReader(bytes.NewReader(b))
	header, err := br.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("read array header: %w", err)
	}
	if !strings.HasPrefix(header, "*") || !strings.HasSuffix(header, "\r\n") {
		return nil, fmt.Errorf("malformed array header %q", header)
	}
	n, err := strconv.Atoi(strings.TrimSuffix(header[1:], "\r\n"))
	if err != nil || n < 0 {
		return nil, fmt.Errorf("bad array count in %q", header)
	}

	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		lenLine, err := br.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("read bulk header %d: %w", i, err)
		}
		if !strings.HasPrefix(lenLine, "$") || !strings.HasSuffix(lenLine, "\r\n") {
			return nil, fmt.Errorf("malformed bulk header %q", lenLine)
		}
		size, err := strconv.Atoi(strings.TrimSuffix(lenLine[1:], "\r\n"))
		if err != nil || size < 0 {
			return nil, fmt.Errorf("bad bulk length in %q", lenLine)
		}
		buf := make([]byte, size)
		if _, err := io_ReadFull(br, buf); err != nil {
			return nil, fmt.Errorf("read bulk body %d: %w", i, err)
		}
		crlf := make([]byte, 2)
		if _, err := io_ReadFull(br, crlf); err != nil {
			return nil, fmt.Errorf("read bulk terminator %d: %w", i, err)
		}
		if string(crlf) != "\r\n" {
			return nil, fmt.Errorf("bulk %d not CRLF-terminated", i)
		}
		out = append(out, string(buf))
	}

	// Nothing may follow the array: trailing bytes would be a second command.
	if _, err := br.ReadByte(); err == nil {
		return nil, fmt.Errorf("trailing bytes after array — possible injected command")
	}
	return out, nil
}

// io_ReadFull avoids importing io just for one helper name collision-free.
func io_ReadFull(br *bufio.Reader, p []byte) (int, error) {
	n := 0
	for n < len(p) {
		m, err := br.Read(p[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// FuzzEncodeRESPNoInjection asserts the COMMAND-INJECTION invariant for the
// Redis ACL rotator: whatever bytes appear in an argument (CRLF, "*3", "$7",
// NULs, a whole forged command), encodeRESP must emit exactly one RESP array
// that decodes back to precisely the original arguments — no more, no fewer.
//
// This matters because the rotator builds `ACL SETUSER <user> ... >password`
// from operator config and a freshly generated secret. If an argument could
// break framing it would append attacker-chosen Redis commands.
func FuzzEncodeRESPNoInjection(f *testing.F) {
	for _, seed := range [][2]string{
		{"ACL", "SETUSER"},
		{"user", ">p4ss"},
		{"inject\r\n*1\r\n$8\r\nFLUSHALL", "x"},
		{"\r\n", "\r\n"},
		{"$5", "*2"},
		{"", ""},
		{"a\x00b", "tail"},
		{"+OK", "-ERR boom"},
		{strings.Repeat("A", 300), "z"},
	} {
		f.Add(seed[0], seed[1])
	}

	f.Fuzz(func(t *testing.T, a, b string) {
		args := []string{"ACL", "SETUSER", a, b}
		wire := encodeRESP(args)

		got, err := parseRESPArray(wire)
		if err != nil {
			t.Fatalf("encodeRESP produced unparseable/ambiguous wire data for %q,%q: %v", a, b, err)
		}
		if len(got) != len(args) {
			t.Fatalf("arg count changed: encoded %d, decoded %d (injection!) for %q,%q",
				len(args), len(got), a, b)
		}
		for i := range args {
			if got[i] != args[i] {
				t.Fatalf("arg %d round-trip mismatch:\n  in  = %q\n  out = %q", i, args[i], got[i])
			}
		}
	})
}
