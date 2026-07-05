package resolve

import (
	"context"
	"fmt"
)

// maxDepth caps reference expansion as a backstop beyond strict cycle detection.
const maxDepth = 32

// Resolver expands inheritance + references over its two ports.
type Resolver struct {
	reader RawReader
	authz  Authorizer // nil = trusted caller (checks skipped)
}

// New builds a Resolver. authz may be nil for trusted internal call sites.
func New(reader RawReader, authz Authorizer) *Resolver {
	return &Resolver{reader: reader, authz: authz}
}

// frame is one (config,key) on the resolution stack, for cycle detection.
type frame struct{ configID, key string }

// run carries per-Resolve mutable state.
type run struct {
	r    *Resolver
	prov map[string]Provenance // configID → provenance (dedup)
}

// Resolve returns rootConfigID's fully merged + dereferenced values, plus the
// distinct target configs read via references. Atomic: any failure returns an
// error and no values (partial output is zeroized).
func (rv *Resolver) Resolve(ctx context.Context, rootConfigID string) (map[string][]byte, []Provenance, error) {
	merged, self, err := resolveMerged(ctx, rv.reader, rootConfigID)
	defer zeroizeMap(merged)
	if err != nil {
		return nil, nil, err
	}
	st := &run{r: rv, prov: map[string]Provenance{}}
	out := make(map[string][]byte, len(merged))
	for k := range merged {
		v, err := st.resolveKey(ctx, self, merged, k, nil)
		if err != nil {
			zeroizeMap(out)
			return nil, nil, err
		}
		out[k] = v
	}
	return out, st.provList(), nil
}

// ResolveKey resolves a single key of rootConfigID (used by single-key reveals).
func (rv *Resolver) ResolveKey(ctx context.Context, rootConfigID, key string) ([]byte, []Provenance, error) {
	merged, self, err := resolveMerged(ctx, rv.reader, rootConfigID)
	defer zeroizeMap(merged)
	if err != nil {
		return nil, nil, err
	}
	if _, ok := merged[key]; !ok {
		return nil, nil, fmt.Errorf("%w: key %q in %s", ErrUnresolvedReference, key, self.Path())
	}
	st := &run{r: rv, prov: map[string]Provenance{}}
	v, err := st.resolveKey(ctx, self, merged, key, nil)
	if err != nil {
		return nil, nil, err
	}
	return v, st.provList(), nil
}

// resolveKey expands one key's value in the context of cfg's merged map. stack
// holds the ancestry of (config,key) frames for cycle detection. It always
// returns a fresh []byte so source maps can be zeroized independently.
func (st *run) resolveKey(ctx context.Context, cfg RawConfig, merged map[string][]byte, key string, stack []frame) ([]byte, error) {
	if len(stack) >= maxDepth {
		return nil, fmt.Errorf("%w: at %s/%s", ErrReferenceDepth, cfg.Path(), key)
	}
	fr := frame{configID: cfg.ConfigID, key: key}
	for _, s := range stack {
		if s == fr {
			return nil, fmt.Errorf("%w: %s/%s", ErrReferenceCycle, cfg.Path(), key)
		}
	}
	raw, ok := merged[key]
	if !ok {
		return nil, fmt.Errorf("%w: key %q in %s", ErrUnresolvedReference, key, cfg.Path())
	}
	segs, err := parseSegments(string(raw))
	if err != nil {
		return nil, err
	}
	// Full-slice expression forces a copy-on-append so sibling references at this
	// level cannot alias each other's frame in a shared backing array.
	next := append(stack[:len(stack):len(stack)], fr)

	// Fast path: a value that is exactly one reference returns the target's exact
	// bytes (binary passthrough).
	if len(segs) == 1 && segs[0].ref != nil {
		return st.resolveRef(ctx, cfg, merged, segs[0].ref, next)
	}
	var buf []byte
	for _, seg := range segs {
		if seg.ref == nil {
			buf = append(buf, seg.literal...)
			continue
		}
		v, err := st.resolveRef(ctx, cfg, merged, seg.ref, next)
		if err != nil {
			zeroize(buf) // discard the partial splice; resolution fails atomically
			return nil, err
		}
		buf = append(buf, v...)
		zeroize(v)
	}
	if buf == nil {
		buf = []byte{} // non-nil empty for an empty value
	}
	return buf, nil
}

// resolveRef resolves one reference token to a fresh []byte.
func (st *run) resolveRef(ctx context.Context, cfg RawConfig, merged map[string][]byte, rf *ref, stack []frame) ([]byte, error) {
	if rf.local {
		return st.resolveKey(ctx, cfg, merged, rf.key, stack)
	}
	target, err := st.r.reader.ReadRaw(ctx, rf.coord)
	if err != nil {
		return nil, fmt.Errorf("%w: %s.%s.%s.%s", ErrUnresolvedReference, rf.coord.Project, rf.coord.Env, rf.coord.Config, rf.key)
	}
	// ReadRaw decrypts the target's own values, but here only its coordinates
	// (for authz + provenance) are needed — the values are re-derived by
	// resolveMerged below. Zeroize this decrypted copy so it does not linger.
	defer zeroizeMap(target.Values)
	if st.r.authz != nil {
		if err := st.r.authz.CanReadSecrets(ctx, target); err != nil {
			return nil, fmt.Errorf("%w: %s.%s.%s.%s", ErrForbiddenReference, rf.coord.Project, rf.coord.Env, rf.coord.Config, rf.key)
		}
	}
	tMerged, tSelf, err := resolveMerged(ctx, st.r.reader, target.ConfigID)
	defer zeroizeMap(tMerged)
	if err != nil {
		return nil, err
	}
	if _, ok := tMerged[rf.key]; !ok {
		return nil, fmt.Errorf("%w: key %q in %s", ErrUnresolvedReference, rf.key, tSelf.Path())
	}
	st.prov[tSelf.ConfigID] = Provenance{ProjectID: tSelf.ProjectID, EnvID: tSelf.EnvID, ConfigID: tSelf.ConfigID, Path: tSelf.Path()}
	return st.resolveKey(ctx, tSelf, tMerged, rf.key, stack)
}

func (st *run) provList() []Provenance {
	out := make([]Provenance, 0, len(st.prov))
	for _, p := range st.prov {
		out = append(out, p)
	}
	return out
}

func zeroize(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
func zeroizeMap(m map[string][]byte) {
	for _, v := range m {
		zeroize(v)
	}
}
