package secrets

import (
	"context"
	"errors"
	"time"

	"github.com/steveokay/janus-secrets/internal/store"
)

// MergedMeta is one key in the inheritance-merged masked view. Origin is
// "own" (defined only here), "inherited" (only from a base), or "overridden"
// (defined here and also in a base).
//
// MaxAgeSeconds/Stale describe the ADVISORY max-age policy (see max_age.go):
// MaxAgeSeconds is the effective policy for the key (per-key override on the
// requested config, else the config default), nil when no policy applies.
// Stale is true when the key's age (now - CreatedAt) exceeds MaxAgeSeconds.
// Purely advisory — never blocks any operation.
//
// LastReadAt/Unused describe ADVISORY unused-secret detection (see
// last_read.go): LastReadAt is the most recent per-key reveal timestamp
// (nil = never read per-key). Unused is true when the key has had no per-key
// reveal within the configured threshold window (JANUS_UNUSED_SECRET_DAYS,
// default 90 days) — either never read, or last read older than the window.
// Purely advisory — never blocks any operation. Value-free (timestamps only).
type MergedMeta struct {
	Key           string
	ValueVersion  int
	CreatedAt     time.Time
	Origin        string
	Type          string
	MaxAgeSeconds *int64
	Stale         bool
	LastReadAt    *time.Time
	Unused        bool
	// Owner/Note are the ADVISORY per-key annotation (see annotations.go): a
	// human-facing owner label and free-text note. nil when unset. Value-free
	// (metadata only) and, like max-age, scoped to the requested (leaf) config —
	// NOT inherited. Purely informational; never blocks any operation.
	Owner *string
	Note  *string
}

type storeMetaEntry struct {
	vv  int
	at  time.Time
	typ string
}

// ListSecretsMerged returns the masked, inheritance-merged key set for configID.
// Metadata only — no decryption, no audit. Child metadata wins; a key present in
// both this config and an ancestor is "overridden".
func (s *Service) ListSecretsMerged(ctx context.Context, configID string) ([]MergedMeta, error) {
	seen := map[string]bool{}
	var chainCV []map[string]storeMetaEntry
	own := map[string]bool{}
	id := configID
	first := true
	for id != "" {
		if seen[id] {
			return nil, ErrConflict // inheritance cycle → 409
		}
		seen[id] = true
		cfg, err := s.configs.Get(ctx, id)
		if err != nil {
			if first {
				return nil, mapStoreErr(err)
			}
			return nil, ErrConflict // broken base → 409
		}
		// A config with no version of its own contributes an empty own-key set
		// but still inherits from its base (a branch that only overrides/adds).
		_, state, err := s.secrets.GetLatest(ctx, id)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, mapStoreErr(err)
		}
		lvl := map[string]storeMetaEntry{}
		for k, sv := range state {
			lvl[k] = storeMetaEntry{vv: sv.ValueVersion, at: sv.CreatedAt, typ: sv.Type}
			if first {
				own[k] = true
			}
		}
		chainCV = append(chainCV, lvl)
		first = false
		if cfg.InheritsFrom != nil {
			id = *cfg.InheritsFrom
		} else {
			id = ""
		}
	}
	// Merge ancestor→child (index len-1 .. 0); child wins for value_version/created_at.
	merged := map[string]storeMetaEntry{}
	presentAtMultiple := map[string]int{}
	for i := len(chainCV) - 1; i >= 0; i-- {
		for k, e := range chainCV[i] {
			merged[k] = e
			presentAtMultiple[k]++
		}
	}
	// Advisory max-age policy is scoped to the requested (leaf) config only —
	// it is NOT inherited (mirrors locked-keys semantics). Effective policy for
	// a key = per-key override if set, else the config default (sentinel), else
	// none. Purely advisory: staleness is a display signal, never enforced.
	entries, err := s.maxAge.List(ctx, configID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	var defSecs *int64
	perKey := map[string]int64{}
	for _, e := range entries {
		if e.Key == store.MaxAgeSentinel {
			v := e.MaxAgeSeconds
			defSecs = &v
		} else {
			perKey[e.Key] = e.MaxAgeSeconds
		}
	}

	// Advisory per-key annotations (owner + note) — one query for the whole
	// config, no N+1. Scoped to the requested (leaf) config, NOT inherited
	// (mirrors max-age / locked-keys). Value-free metadata; never blocks anything.
	annEntries, err := s.annots.List(ctx, configID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	annOwner := map[string]*string{}
	annNote := map[string]*string{}
	for _, e := range annEntries {
		annOwner[e.Key] = e.Owner
		annNote[e.Key] = e.Note
	}

	// Advisory unused-secret detection: last per-key reveal timestamp for each
	// key, from one grouped audit query (value-free — resource paths + times).
	// Attributed to the requested (leaf) config only; inherited keys have no
	// last-read on this config and so read as "never read" here. Never blocks.
	lastReads, err := s.lastRead.LastReadByKey(ctx, configID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	now := time.Now()
	unusedCutoff := now.Add(-s.unusedWindow())

	out := make([]MergedMeta, 0, len(merged))
	for k, e := range merged {
		origin := "inherited"
		if own[k] {
			if presentAtMultiple[k] > 1 {
				origin = "overridden"
			} else {
				origin = "own"
			}
		}
		m := MergedMeta{Key: k, ValueVersion: e.vv, CreatedAt: e.at, Origin: origin, Type: e.typ}
		if secs, ok := perKey[k]; ok {
			v := secs
			m.MaxAgeSeconds = &v
		} else if defSecs != nil {
			m.MaxAgeSeconds = defSecs
		}
		if m.MaxAgeSeconds != nil {
			m.Stale = now.Sub(e.at) > time.Duration(*m.MaxAgeSeconds)*time.Second
		}
		if at, ok := lastReads[k]; ok {
			v := at
			m.LastReadAt = &v
			m.Unused = at.Before(unusedCutoff) // read, but outside the window (stale read)
		} else {
			m.Unused = true // never read per-key
		}
		m.Owner = annOwner[k]
		m.Note = annNote[k]
		out = append(out, m)
	}
	return out, nil
}
