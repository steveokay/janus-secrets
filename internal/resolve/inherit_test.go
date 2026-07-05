package resolve

import (
	"context"
	"errors"
	"testing"
)

// fakeReader serves RawConfigs from an in-memory map keyed by config id, plus a
// coord index for ReadRaw. Missing id → notFoundErr.
type fakeReader struct {
	byID    map[string]RawConfig
	byCoord map[Coord]string // coord → config id
}

var errFakeNotFound = errors.New("fake: not found")

func (f *fakeReader) ReadRawByID(_ context.Context, id string) (RawConfig, error) {
	rc, ok := f.byID[id]
	if !ok {
		return RawConfig{}, errFakeNotFound
	}
	return rc, nil
}
func (f *fakeReader) ReadRaw(_ context.Context, c Coord) (RawConfig, error) {
	id, ok := f.byCoord[c]
	if !ok {
		return RawConfig{}, errFakeNotFound
	}
	return f.ReadRawByID(context.Background(), id)
}

func strptr(s string) *string { return &s }

func TestResolveMergedChildWins(t *testing.T) {
	r := &fakeReader{byID: map[string]RawConfig{
		"base":   {ConfigID: "base", Values: map[string][]byte{"A": []byte("1"), "B": []byte("2")}},
		"branch": {ConfigID: "branch", InheritsFrom: strptr("base"), Values: map[string][]byte{"B": []byte("20"), "C": []byte("3")}},
	}}
	merged, self, err := resolveMerged(context.Background(), r, "branch")
	if err != nil {
		t.Fatal(err)
	}
	if self.ConfigID != "branch" {
		t.Fatalf("self = %s", self.ConfigID)
	}
	want := map[string]string{"A": "1", "B": "20", "C": "3"} // child B wins
	if len(merged) != len(want) {
		t.Fatalf("merged keys = %v", merged)
	}
	for k, v := range want {
		if string(merged[k]) != v {
			t.Fatalf("merged[%s] = %q, want %q", k, merged[k], v)
		}
	}
}

func TestResolveMergedCycle(t *testing.T) {
	r := &fakeReader{byID: map[string]RawConfig{
		"a": {ConfigID: "a", InheritsFrom: strptr("b"), Values: map[string][]byte{}},
		"b": {ConfigID: "b", InheritsFrom: strptr("a"), Values: map[string][]byte{}},
	}}
	if _, _, err := resolveMerged(context.Background(), r, "a"); !errors.Is(err, ErrInheritanceCycle) {
		t.Fatalf("want ErrInheritanceCycle, got %v", err)
	}
}

func TestResolveMergedBrokenBaseVsMissingRoot(t *testing.T) {
	r := &fakeReader{byID: map[string]RawConfig{
		"branch": {ConfigID: "branch", InheritsFrom: strptr("gone"), Values: map[string][]byte{}},
	}}
	// Missing base → ErrBrokenInheritance.
	if _, _, err := resolveMerged(context.Background(), r, "branch"); !errors.Is(err, ErrBrokenInheritance) {
		t.Fatalf("broken base: want ErrBrokenInheritance, got %v", err)
	}
	// Missing root → original reader error propagates (not ErrBrokenInheritance).
	if _, _, err := resolveMerged(context.Background(), r, "gone"); !errors.Is(err, errFakeNotFound) {
		t.Fatalf("missing root: want errFakeNotFound, got %v", err)
	}
}
