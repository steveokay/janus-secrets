package secrets

import (
	"context"
	"fmt"
	"strings"
)

// Annotation length bounds (mirror the DB CHECK constraints). Enforced at the
// service boundary so a too-long value is a clean ErrValidation, not a DB error.
const (
	MaxAnnotationOwnerLen = 256
	MaxAnnotationNoteLen  = 2048
)

// Annotation is one per-key secret annotation: an owner label and/or a free-text
// note. Value-free — human-facing metadata only, never secret material. Owner and
// Note are nil when unset.
type Annotation struct {
	Key   string
	Owner *string
	Note  *string
}

// SetAnnotation sets (or clears) the owner + note annotation for a key. owner and
// note are trimmed; an empty string is treated as "unset" for that field. If both
// end up empty the annotation is cleared entirely. key must be a valid secret key.
// Returns the normalized owner/note that were stored (both nil on a clear) and
// whether the resulting state is a clear (true) or a set (false), so the caller
// can echo the stored values and emit the right audit action.
func (s *Service) SetAnnotation(ctx context.Context, configID, key string, owner, note *string, actor string) (outOwner, outNote *string, cleared bool, err error) {
	if err := validateKey(key); err != nil {
		return nil, nil, false, err
	}
	o := normalizeAnnField(owner)
	n := normalizeAnnField(note)
	if o != nil && len(*o) > MaxAnnotationOwnerLen {
		return nil, nil, false, fmt.Errorf("%w: owner exceeds maximum length", ErrValidation)
	}
	if n != nil && len(*n) > MaxAnnotationNoteLen {
		return nil, nil, false, fmt.Errorf("%w: note exceeds maximum length", ErrValidation)
	}
	if _, err := s.configs.Get(ctx, configID); err != nil {
		return nil, nil, false, mapStoreErr(err)
	}
	if o == nil && n == nil {
		return nil, nil, true, mapStoreErr(s.annots.Clear(ctx, configID, key))
	}
	return o, n, false, mapStoreErr(s.annots.Set(ctx, configID, key, o, n, actor))
}

// ClearAnnotation removes a key's annotation. Clearing an absent annotation is a
// no-op. key must be a valid secret key.
func (s *Service) ClearAnnotation(ctx context.Context, configID, key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if _, err := s.configs.Get(ctx, configID); err != nil {
		return mapStoreErr(err)
	}
	return mapStoreErr(s.annots.Clear(ctx, configID, key))
}

// ListAnnotations returns a config's per-key annotations.
func (s *Service) ListAnnotations(ctx context.Context, configID string) ([]Annotation, error) {
	if _, err := s.configs.Get(ctx, configID); err != nil {
		return nil, mapStoreErr(err)
	}
	entries, err := s.annots.List(ctx, configID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	out := make([]Annotation, 0, len(entries))
	for _, e := range entries {
		out = append(out, Annotation{Key: e.Key, Owner: e.Owner, Note: e.Note})
	}
	return out, nil
}

// normalizeAnnField trims whitespace and maps an empty result to nil (unset).
func normalizeAnnField(v *string) *string {
	if v == nil {
		return nil
	}
	t := strings.TrimSpace(*v)
	if t == "" {
		return nil
	}
	return &t
}
