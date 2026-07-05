package resolve

import (
	"context"
	"errors"
	"testing"
)

// denyAuthorizer denies a specific config id; allows the rest.
type denyAuthorizer struct{ denyID string }

func (d denyAuthorizer) CanReadSecrets(_ context.Context, t RawConfig) error {
	if t.ConfigID == d.denyID {
		return ErrForbiddenReference
	}
	return nil
}

func newReader() *fakeReader {
	return &fakeReader{byID: map[string]RawConfig{}, byCoord: map[Coord]string{}}
}
func (f *fakeReader) put(rc RawConfig, coord Coord) {
	f.byID[rc.ConfigID] = rc
	f.byCoord[coord] = rc.ConfigID
}

func TestResolveLocalAndAbsolute(t *testing.T) {
	r := newReader()
	r.put(RawConfig{ProjectID: "pB", EnvID: "eB", ConfigID: "cB", Project: "billing", Env: "prod", Config: "api",
		Values: map[string][]byte{"HOST": []byte("db.internal")}}, Coord{"billing", "prod", "api"})
	r.put(RawConfig{ProjectID: "pA", EnvID: "eA", ConfigID: "cA", Project: "app", Env: "prod", Config: "web",
		Values: map[string][]byte{
			"USER": []byte("svc"),
			"URL":  []byte("u://${USER}@${projects.billing.prod.api.HOST}/x"),
		}}, Coord{"app", "prod", "web"})

	res := &Resolver{reader: r, authz: nil}
	out, prov, err := res.Resolve(context.Background(), "cA")
	if err != nil {
		t.Fatal(err)
	}
	if string(out["URL"]) != "u://svc@db.internal/x" {
		t.Fatalf("URL = %q", out["URL"])
	}
	if len(prov) != 1 || prov[0].ConfigID != "cB" {
		t.Fatalf("provenance = %+v", prov)
	}
}

func TestResolveForbiddenReference(t *testing.T) {
	r := newReader()
	r.put(RawConfig{ConfigID: "cB", Project: "billing", Env: "prod", Config: "api",
		Values: map[string][]byte{"K": []byte("v")}}, Coord{"billing", "prod", "api"})
	r.put(RawConfig{ConfigID: "cA", Project: "app", Env: "prod", Config: "web",
		Values: map[string][]byte{"X": []byte("${projects.billing.prod.api.K}")}}, Coord{"app", "prod", "web"})

	res := &Resolver{reader: r, authz: denyAuthorizer{denyID: "cB"}}
	if _, _, err := res.Resolve(context.Background(), "cA"); !errors.Is(err, ErrForbiddenReference) {
		t.Fatalf("want ErrForbiddenReference, got %v", err)
	}
}

func TestResolveReferenceCycle(t *testing.T) {
	r := newReader()
	r.put(RawConfig{ConfigID: "cA", Project: "a", Env: "p", Config: "x",
		Values: map[string][]byte{"A": []byte("${projects.b.p.y.B}")}}, Coord{"a", "p", "x"})
	r.put(RawConfig{ConfigID: "cB", Project: "b", Env: "p", Config: "y",
		Values: map[string][]byte{"B": []byte("${projects.a.p.x.A}")}}, Coord{"b", "p", "y"})
	res := &Resolver{reader: r, authz: nil}
	if _, _, err := res.Resolve(context.Background(), "cA"); !errors.Is(err, ErrReferenceCycle) {
		t.Fatalf("want ErrReferenceCycle, got %v", err)
	}
}

func TestResolveUnresolvedReference(t *testing.T) {
	r := newReader()
	r.put(RawConfig{ConfigID: "cA", Project: "a", Env: "p", Config: "x",
		Values: map[string][]byte{"A": []byte("${MISSING}")}}, Coord{"a", "p", "x"})
	res := &Resolver{reader: r, authz: nil}
	if _, _, err := res.Resolve(context.Background(), "cA"); !errors.Is(err, ErrUnresolvedReference) {
		t.Fatalf("local missing key: want ErrUnresolvedReference, got %v", err)
	}
}

func TestResolveKeyBinaryPassthrough(t *testing.T) {
	// A value that is exactly one reference yields the target's exact bytes.
	r := newReader()
	bin := []byte{0x00, 0xff, 0x10}
	r.put(RawConfig{ConfigID: "cB", Project: "b", Env: "p", Config: "y",
		Values: map[string][]byte{"BIN": bin}}, Coord{"b", "p", "y"})
	r.put(RawConfig{ConfigID: "cA", Project: "a", Env: "p", Config: "x",
		Values: map[string][]byte{"K": []byte("${projects.b.p.y.BIN}")}}, Coord{"a", "p", "x"})
	res := &Resolver{reader: r, authz: nil}
	got, _, err := res.ResolveKey(context.Background(), "cA", "K")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(bin) {
		t.Fatalf("binary passthrough = %v", got)
	}
}
