package resolve

import (
	"errors"
	"testing"
)

func TestSentinelsAreDistinct(t *testing.T) {
	all := []error{
		ErrInheritanceCycle, ErrBrokenInheritance, ErrReferenceCycle,
		ErrUnresolvedReference, ErrForbiddenReference, ErrReferenceDepth,
		ErrBadReferenceSyntax,
	}
	for i := range all {
		for j := range all {
			if i != j && errors.Is(all[i], all[j]) {
				t.Fatalf("sentinels %d and %d must be distinct", i, j)
			}
		}
	}
}

func TestCoordAndRawConfigZeroValues(t *testing.T) {
	var c Coord
	if c.Project != "" || c.Env != "" || c.Config != "" {
		t.Fatal("zero Coord must be empty")
	}
	rc := RawConfig{Values: map[string][]byte{}}
	if rc.InheritsFrom != nil || len(rc.Values) != 0 {
		t.Fatal("zero RawConfig unexpected")
	}
}
