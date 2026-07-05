package resolve

import (
	"context"
	"fmt"
)

// resolveMerged returns configID's inheritance-merged raw values (child wins) and
// the requested config's own RawConfig (identity). It walks InheritsFrom with a
// cycle guard. A missing requested config propagates the reader's error; a
// missing/unreadable *base* in the chain is ErrBrokenInheritance.
//
// The returned map's []byte values are the reader's decrypted plaintext; the
// caller owns them and must zero them when done.
func resolveMerged(ctx context.Context, reader RawReader, configID string) (map[string][]byte, RawConfig, error) {
	seen := map[string]bool{}
	var chain []RawConfig
	id := configID
	for id != "" {
		if seen[id] {
			return nil, RawConfig{}, fmt.Errorf("%w: at config %s", ErrInheritanceCycle, id)
		}
		seen[id] = true
		rc, err := reader.ReadRawByID(ctx, id)
		if err != nil {
			if len(chain) == 0 {
				return nil, RawConfig{}, err // requested config missing: propagate (→ 404)
			}
			return nil, RawConfig{}, fmt.Errorf("%w: base %s", ErrBrokenInheritance, id)
		}
		chain = append(chain, rc)
		if rc.InheritsFrom != nil {
			id = *rc.InheritsFrom
		} else {
			id = ""
		}
	}
	// chain[0] = requested (child); chain[len-1] = deepest ancestor. Apply
	// ancestor→child so the child's keys win.
	merged := make(map[string][]byte)
	for i := len(chain) - 1; i >= 0; i-- {
		for k, v := range chain[i].Values {
			merged[k] = v
		}
	}
	return merged, chain[0], nil
}
