package secrets

import (
	"context"
	"fmt"
	"strings"
)

// Annotation length bounds (mirror the DB CHECK constraints). Enforced at the
// service boundary so a too-long value is a clean ErrValidation, not a DB error.
const (
	// MaxProjectOwnerLen bounds the project's advisory owner label. It lives on
	// the project, not the key: a service has an owner, its individual keys
	// almost never do (migration 000049).
	MaxProjectOwnerLen   = 256
	MaxAnnotationNoteLen = 2048
)

// Annotation is one per-key secret note — free text such as "read replica,
// rotate with the primary". Value-free: human-facing metadata only, never
// secret material. Note is nil when unset.
type Annotation struct {
	Key  string
	Note *string
}

// SetAnnotation sets (or clears) a key's note. The note is trimmed; an empty
// string is treated as "unset" and clears the annotation entirely. key must be a
// valid secret key. Returns the normalized note that was stored (nil on a clear)
// and whether the resulting state is a clear, so the caller can echo the stored
// value and emit the right audit action.
func (s *Service) SetAnnotation(ctx context.Context, configID, key string, note *string, actor string) (outNote *string, cleared bool, err error) {
	if err := validateKey(key); err != nil {
		return nil, false, err
	}
	n := normalizeAnnField(note)
	if n != nil && len(*n) > MaxAnnotationNoteLen {
		return nil, false, fmt.Errorf("%w: note exceeds maximum length", ErrValidation)
	}
	if _, err := s.configs.Get(ctx, configID); err != nil {
		return nil, false, mapStoreErr(err)
	}
	if n == nil {
		return nil, true, mapStoreErr(s.annots.Clear(ctx, configID, key))
	}
	return n, false, mapStoreErr(s.annots.Set(ctx, configID, key, n, actor))
}

// SetProjectOwner sets or clears a project's advisory owner label. Trimmed; an
// empty string clears it. ADVISORY ONLY — a display label answering "who do I
// ask about this service". It grants nothing and is never consulted in an
// authorization decision; real ownership is a role binding.
func (s *Service) SetProjectOwner(ctx context.Context, projectID string, owner *string) (*string, error) {
	o := normalizeAnnField(owner)
	if o != nil && len(*o) > MaxProjectOwnerLen {
		return nil, fmt.Errorf("%w: owner exceeds maximum length", ErrValidation)
	}
	if err := s.projects.UpdateOwner(ctx, projectID, o); err != nil {
		return nil, mapStoreErr(err)
	}
	return o, nil
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
		out = append(out, Annotation{Key: e.Key, Note: e.Note})
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
