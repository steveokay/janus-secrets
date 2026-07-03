package store

import "time"

// EncryptedValue is an opaque encrypted secret value. The store persists and
// returns these bytes verbatim; it never inspects or decrypts them.
type EncryptedValue struct {
	WrappedDEK    []byte
	Ciphertext    []byte
	Nonce         []byte
	DEKKeyVersion int
}

// Project is the top of the hierarchy and owns a wrapped project KEK.
type Project struct {
	ID         string
	Slug       string
	Name       string
	WrappedKEK []byte
	KEKVersion int
	CreatedAt  time.Time
	UpdatedAt  time.Time
	DeletedAt  *time.Time
}

// Environment is a user-definable environment within a project (dev/staging/prod).
type Environment struct {
	ID        string
	ProjectID string
	Slug      string
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
}

// Config holds secrets within an environment. InheritsFrom is reserved for the
// not-yet-implemented inheritance feature.
type Config struct {
	ID            string
	EnvironmentID string
	Name          string
	InheritsFrom  *string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	DeletedAt     *time.Time
}

// ConfigVersion is one immutable save — the unit of diff and rollback.
type ConfigVersion struct {
	ID        string
	ConfigID  string
	Version   int
	Message   string
	CreatedBy string
	CreatedAt time.Time
}

// SecretValue is one immutable, append-only value in a key's history.
type SecretValue struct {
	ID           string
	ConfigID     string
	Key          string
	ValueVersion int
	EncryptedValue
	CreatedAt time.Time
}

// Change is one edit within a batched save. Value == nil means delete the key
// (a tombstone); otherwise it sets the key to the given encrypted value.
type Change struct {
	Key   string
	Value *EncryptedValue
}

// Diff is the set difference between two config versions.
type Diff struct {
	Added   []string
	Changed []string
	Removed []string
}
