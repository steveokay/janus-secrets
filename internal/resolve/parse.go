package resolve

import (
	"fmt"
	"strings"
)

// ref is a parsed reference token. local ${KEY} sets local=true and key; an
// absolute ${projects.p.e.c.KEY} sets coord and key.
type ref struct {
	local bool
	coord Coord
	key   string
}

// segment is either literal text (ref == nil) or a reference (literal == "").
type segment struct {
	literal string
	ref     *ref
}

// parseSegments splits value into literal/reference segments. $$ is a literal $;
// ${...} is a reference; an unterminated ${ or malformed body is
// ErrBadReferenceSyntax.
func parseSegments(value string) ([]segment, error) {
	var out []segment
	for i := 0; i < len(value); {
		d := strings.IndexByte(value[i:], '$')
		if d < 0 {
			out = append(out, segment{literal: value[i:]})
			break
		}
		if d > 0 {
			out = append(out, segment{literal: value[i : i+d]})
		}
		j := i + d // position of '$'
		switch {
		case j+1 < len(value) && value[j+1] == '$':
			out = append(out, segment{literal: "$"})
			i = j + 2
		case j+1 < len(value) && value[j+1] == '{':
			end := strings.IndexByte(value[j+2:], '}')
			if end < 0 {
				return nil, fmt.Errorf("%w: unterminated ${", ErrBadReferenceSyntax)
			}
			body := value[j+2 : j+2+end]
			rf, err := parseRefBody(body)
			if err != nil {
				return nil, err
			}
			out = append(out, segment{ref: rf})
			i = j + 2 + end + 1
		default:
			out = append(out, segment{literal: "$"})
			i = j + 1
		}
	}
	return out, nil
}

func parseRefBody(body string) (*ref, error) {
	parts := strings.Split(body, ".")
	switch {
	case len(parts) == 1:
		if !validSegmentToken(parts[0]) {
			return nil, fmt.Errorf("%w: bad local key %q", ErrBadReferenceSyntax, body)
		}
		return &ref{local: true, key: parts[0]}, nil
	case len(parts) == 5 && parts[0] == "projects":
		for _, p := range parts[1:] {
			if !validSegmentToken(p) {
				return nil, fmt.Errorf("%w: bad reference %q", ErrBadReferenceSyntax, body)
			}
		}
		return &ref{coord: Coord{Project: parts[1], Env: parts[2], Config: parts[3]}, key: parts[4]}, nil
	default:
		return nil, fmt.Errorf("%w: %q must be KEY or projects.p.e.c.KEY", ErrBadReferenceSyntax, body)
	}
}

// validSegmentToken permits non-empty tokens of letters, digits, '_' and '-'.
// (Existence and finer key/slug rules are enforced at resolution time by the
// store lookups; this only rejects obviously malformed tokens.)
func validSegmentToken(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		ok := c == '_' || c == '-' ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		if !ok {
			return false
		}
	}
	return true
}
