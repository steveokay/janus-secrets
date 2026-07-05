package resolve

import (
	"errors"
	"reflect"
	"testing"
)

func TestParseSegments(t *testing.T) {
	loc := func(k string) segment { return segment{ref: &ref{local: true, key: k}} }
	abs := func(p, e, c, k string) segment {
		return segment{ref: &ref{coord: Coord{p, e, c}, key: k}}
	}
	lit := func(s string) segment { return segment{literal: s} }

	cases := []struct {
		name string
		in   string
		want []segment
	}{
		{"plain", "hello", []segment{lit("hello")}},
		{"local", "${DB}", []segment{loc("DB")}},
		{"absolute", "${projects.billing.prod.api.KEY}", []segment{abs("billing", "prod", "api", "KEY")}},
		{"interleaved", "u://${U}:${P}@${projects.i.prod.db.HOST}/x", []segment{
			lit("u://"), loc("U"), lit(":"), loc("P"), lit("@"), abs("i", "prod", "db", "HOST"), lit("/x"),
		}},
		{"escape", "$${DB}", []segment{lit("$"), lit("{DB}")}},
		{"lone dollar", "cost is $5", []segment{lit("cost is "), lit("$"), lit("5")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseSegments(tc.in)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parseSegments(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseSegmentsErrors(t *testing.T) {
	for _, in := range []string{"${", "${unterminated", "${ }", "${a.b.c.d}", "${projects.a.b.KEY}", "${projects.a.b.c.d.e.KEY}"} {
		if _, err := parseSegments(in); !errors.Is(err, ErrBadReferenceSyntax) {
			t.Fatalf("parseSegments(%q): want ErrBadReferenceSyntax, got %v", in, err)
		}
	}
}
