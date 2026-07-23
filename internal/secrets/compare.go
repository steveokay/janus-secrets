package secrets

import (
	"context"
	"errors"
)

// CompareRow is one key in a value-free cross-config comparison. It carries
// only booleans and metadata — NEVER a secret value. InA/InB report presence of
// the key in each config; Differs is true only when the key is present in BOTH
// and the resolved plaintext values are unequal. OriginA/OriginB mirror the
// masked-list Origin ("own"/"inherited"/"overridden"), or "" when the key is
// absent on that side.
type CompareRow struct {
	Key     string
	InA     bool
	InB     bool
	Differs bool
	OriginA string
	OriginB string
}

// CompareConfigs computes the value-free key-level diff between two arbitrary
// configs. It reveals both configs' plaintext (the same reveal path promotion's
// preview uses), compares each value IN MEMORY, and returns ONLY booleans + key
// names + per-side origins. No secret value is ever returned or logged.
//
// The caller must have authorized SecretRead on BOTH configs and is responsible
// for emitting the value-free compare audit event. This method mirrors
// promote.Service.Preview's resolve-both-then-compare template, generalized to
// any two configs (no pipeline-step constraint).
func (s *Service) CompareConfigs(ctx context.Context, configA, configB string) ([]CompareRow, error) {
	valsA, err := s.revealForCompare(ctx, configA)
	if err != nil {
		return nil, err
	}
	defer zeroizeSecretMap(valsA)
	valsB, err := s.revealForCompare(ctx, configB)
	if err != nil {
		return nil, err
	}
	defer zeroizeSecretMap(valsB)

	// Origins are cheap masked metadata (no decryption). A metadata failure must
	// not leak values; treat it as "no origin info" rather than aborting the
	// value comparison.
	originA := s.originMap(ctx, configA)
	originB := s.originMap(ctx, configB)

	seen := map[string]bool{}
	rows := make([]CompareRow, 0, len(valsA)+len(valsB))
	add := func(key string) {
		if seen[key] {
			return
		}
		seen[key] = true
		a, inA := valsA[key]
		b, inB := valsB[key]
		row := CompareRow{
			Key:     key,
			InA:     inA,
			InB:     inB,
			OriginA: originA[key],
			OriginB: originB[key],
		}
		if inA && inB {
			// Differs is the ONLY place values are compared, and only booleans
			// escape this scope. string(...) == string(...) is a value comparison,
			// never a value emission.
			row.Differs = string(a.Value) != string(b.Value)
		}
		rows = append(rows, row)
	}
	for k := range valsA {
		add(k)
	}
	for k := range valsB {
		add(k)
	}
	return rows, nil
}

// revealForCompare reveals a config's latest plaintext, treating a config that
// has no version yet as empty (mirrors promote.Preview): all of the other
// side's keys then read as only-there rather than the compare failing. The
// config must still exist — a missing/deleted config id surfaces its error.
func (s *Service) revealForCompare(ctx context.Context, configID string) (map[string]Secret, error) {
	_, vals, err := s.RevealConfig(ctx, configID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return map[string]Secret{}, nil
		}
		return nil, err
	}
	return vals, nil
}

// originMap returns key→origin ("own"/"inherited"/"overridden") from the masked
// merged view. Best-effort: on error it returns an empty map so a compare never
// fails on advisory metadata.
func (s *Service) originMap(ctx context.Context, configID string) map[string]string {
	metas, err := s.ListSecretsMerged(ctx, configID)
	if err != nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(metas))
	for _, m := range metas {
		out[m.Key] = m.Origin
	}
	return out
}

// zeroizeSecretMap best-effort wipes the decrypted plaintext of a reveal map.
func zeroizeSecretMap(m map[string]Secret) {
	for _, sec := range m {
		zeroize(sec.Value)
	}
}
