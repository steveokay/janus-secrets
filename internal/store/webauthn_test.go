package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// mkWebAuthnUser creates a user and returns its id.
func mkWebAuthnUser(t *testing.T, s *Store, email string) string {
	t.Helper()
	hash := "$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA"
	u, err := NewUserRepo(s).Create(context.Background(), email, &hash)
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func TestWebAuthnRepo_ChallengeIsSingleUseAndExpires(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	repo := NewWebAuthnRepo(s)
	uid := mkWebAuthnUser(t, s, "wa-chal@example.com")

	tests := []struct {
		name      string
		challenge string
		purpose   string
		userID    *string
		expires   time.Time
		claimAs   string // purpose used when claiming
		wantFound bool
	}{
		{"live registration challenge", "chal-live", "register", &uid, time.Now().Add(time.Minute), "register", true},
		{"expired challenge", "chal-expired", "register", &uid, time.Now().Add(-time.Second), "register", false},
		{"purpose mismatch", "chal-purpose", "register", &uid, time.Now().Add(time.Minute), "login", false},
		{"decoy challenge with no user", "chal-decoy", "login", nil, time.Now().Add(time.Minute), "login", true},
		// The passwordless pool: always user-less, and it must not be reachable
		// from the identified pool in either direction.
		{"discoverable challenge", "chal-disc", "login_discoverable", nil, time.Now().Add(time.Minute), "login_discoverable", true},
		{"expired discoverable challenge", "chal-disc-old", "login_discoverable", nil, time.Now().Add(-time.Second), "login_discoverable", false},
		{"discoverable claimed as identified", "chal-disc-x", "login_discoverable", nil, time.Now().Add(time.Minute), "login", false},
		{"identified claimed as discoverable", "chal-id-x", "login", &uid, time.Now().Add(time.Minute), "login_discoverable", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := repo.InsertChallenge(ctx, tc.challenge, tc.purpose, tc.userID, []byte(`{"challenge":"x"}`), tc.expires); err != nil {
				t.Fatalf("insert: %v", err)
			}
			row, err := repo.ClaimChallenge(ctx, tc.challenge, tc.claimAs)
			if !tc.wantFound {
				if !errors.Is(err, ErrNotFound) {
					t.Fatalf("claim: want ErrNotFound, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("claim: %v", err)
			}
			if row.Challenge != tc.challenge || row.Purpose != tc.purpose {
				t.Fatalf("claimed row mismatch: %+v", row)
			}
			if (row.UserID == nil) != (tc.userID == nil) {
				t.Fatalf("user binding lost: %+v", row.UserID)
			}
			// Single use: the row is gone.
			if _, err := repo.ClaimChallenge(ctx, tc.challenge, tc.claimAs); !errors.Is(err, ErrNotFound) {
				t.Fatalf("second claim: want ErrNotFound, got %v", err)
			}
		})
	}

	// A duplicate challenge value is a unique violation, never a silent
	// overwrite of a live ceremony.
	if err := repo.InsertChallenge(ctx, "chal-dup", "login", &uid, []byte(`{}`), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := repo.InsertChallenge(ctx, "chal-dup", "login", &uid, []byte(`{}`), time.Now().Add(time.Minute)); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate challenge: want ErrAlreadyExists, got %v", err)
	}

	// The sweeper removes expired rows and leaves live ones.
	if err := repo.InsertChallenge(ctx, "chal-old", "login", &uid, []byte(`{}`), time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteExpiredChallenges(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if _, err := repo.ClaimChallenge(ctx, "chal-old", "login"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired row survived the sweep: %v", err)
	}
	if _, err := repo.ClaimChallenge(ctx, "chal-dup", "login"); err != nil {
		t.Fatalf("sweep removed a live challenge: %v", err)
	}
}

// Concurrent claims of the same challenge: exactly one may win.
func TestWebAuthnRepo_ConcurrentChallengeClaim(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	repo := NewWebAuthnRepo(s)
	uid := mkWebAuthnUser(t, s, "wa-race@example.com")

	if err := repo.InsertChallenge(ctx, "chal-race", "login", &uid, []byte(`{}`), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	const n = 8
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		wins int
	)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := repo.ClaimChallenge(ctx, "chal-race", "login"); err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("%d concurrent claims succeeded, want exactly 1", wins)
	}
}

func TestWebAuthnRepo_CredentialCRUDAndScoping(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	repo := NewWebAuthnRepo(s)
	uid := mkWebAuthnUser(t, s, "wa-crud@example.com")
	other := mkWebAuthnUser(t, s, "wa-other@example.com")

	cred, err := repo.InsertCredential(ctx, uid, "example.com", []byte("cred-1"), []byte(`{"id":"x"}`), 3, "Laptop", nil)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if cred.SignCount != 3 || cred.LastUsedAt != nil {
		t.Fatalf("fresh credential = %+v", cred)
	}
	// Discoverability is UNKNOWN when the client did not report credProps — not
	// false. The UI must be able to tell "we know it is device-bound" apart from
	// "we never found out".
	if cred.Discoverable != nil {
		t.Fatalf("unreported discoverability = %v, want nil (unknown)", *cred.Discoverable)
	}
	// A successful passwordless assertion is proof of discoverability and
	// promotes unknown → true; it is idempotent and never moves back.
	if err := repo.MarkDiscoverable(ctx, cred.ID); err != nil {
		t.Fatalf("mark discoverable: %v", err)
	}
	if err := repo.MarkDiscoverable(ctx, cred.ID); err != nil {
		t.Fatalf("mark discoverable (repeat): %v", err)
	}
	if got, err := repo.GetCredentialByCredentialID(ctx, []byte("cred-1")); err != nil ||
		got.Discoverable == nil || !*got.Discoverable {
		t.Fatalf("after MarkDiscoverable: %v (%+v)", err, got)
	}

	// A credential id is globally unique â€” the same authenticator cannot be
	// registered twice, not even to a different account.
	if _, err := repo.InsertCredential(ctx, other, "example.com", []byte("cred-1"), []byte(`{}`), 0, "Stolen", nil); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate credential id: want ErrAlreadyExists, got %v", err)
	}
	// Nicknames are unique per user, case-insensitively.
	if _, err := repo.InsertCredential(ctx, uid, "example.com", []byte("cred-2"), []byte(`{}`), 0, "laptop", nil); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate nickname: want ErrAlreadyExists, got %v", err)
	}

	// Listing is scoped to the RP ID: a credential registered under a different
	// relying party is not usable and must not be offered.
	if _, err := repo.InsertCredential(ctx, uid, "other.example.com", []byte("cred-3"), []byte(`{}`), 0, "Elsewhere", nil); err != nil {
		t.Fatal(err)
	}
	// An explicitly-reported credProps.rk survives the round trip in both
	// directions, distinctly from nil.
	no := false
	bound, err := repo.InsertCredential(ctx, uid, "example.com", []byte("cred-4"), []byte(`{}`), 0, "Device bound", &no)
	if err != nil {
		t.Fatal(err)
	}
	if bound.Discoverable == nil || *bound.Discoverable {
		t.Fatalf("credProps.rk=false round trip = %+v", bound.Discoverable)
	}
	if err := repo.DeleteCredential(ctx, bound.ID, uid); err != nil {
		t.Fatal(err)
	}
	rows, err := repo.ListCredentials(ctx, uid, "example.com")
	if err != nil || len(rows) != 1 {
		t.Fatalf("list: %v (%d rows)", err, len(rows))
	}
	if n, err := repo.CountCredentials(ctx, uid, "example.com"); err != nil || n != 1 {
		t.Fatalf("count: %v (%d)", err, n)
	}

	got, err := repo.GetCredentialByCredentialID(ctx, []byte("cred-1"))
	if err != nil || got.ID != cred.ID {
		t.Fatalf("lookup by credential id: %v (%+v)", err, got)
	}
	if _, err := repo.GetCredentialByCredentialID(ctx, []byte("nope")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown credential id: want ErrNotFound, got %v", err)
	}

	// Rename and delete are scoped to the owner.
	if err := repo.RenameCredential(ctx, cred.ID, other, "mine now"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user rename: want ErrNotFound, got %v", err)
	}
	if err := repo.DeleteCredential(ctx, cred.ID, other); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user delete: want ErrNotFound, got %v", err)
	}
	if err := repo.RenameCredential(ctx, cred.ID, uid, "Desk laptop"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if err := repo.DeleteCredential(ctx, cred.ID, uid); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := repo.GetCredentialByCredentialID(ctx, []byte("cred-1")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("credential survived delete: %v", err)
	}
}

// RecordAssertion is the clone detector: the counter must move strictly
// forward, except for authenticators that permanently report zero.
func TestWebAuthnRepo_RecordAssertionEnforcesCounter(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	repo := NewWebAuthnRepo(s)
	uid := mkWebAuthnUser(t, s, "wa-counter@example.com")

	counting, err := repo.InsertCredential(ctx, uid, "example.com", []byte("c-count"), []byte(`{}`), 5, "counting", nil)
	if err != nil {
		t.Fatal(err)
	}
	zeroing, err := repo.InsertCredential(ctx, uid, "example.com", []byte("c-zero"), []byte(`{}`), 0, "counterless", nil)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		id       string
		newCount int64
		want     bool
	}{
		{"advance past the stored counter", counting.ID, 6, true},
		{"same counter is a replay", counting.ID, 6, false},
		{"lower counter is a clone", counting.ID, 2, false},
		{"zero against a counting authenticator is a clone", counting.ID, 0, false},
		{"a permanently-zero authenticator keeps working", zeroing.ID, 0, true},
		{"and again", zeroing.ID, 0, true},
		{"a zero authenticator may still start counting", zeroing.ID, 1, true},
		{"but not go back to zero", zeroing.ID, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := repo.RecordAssertion(ctx, tc.id, []byte(`{"seen":true}`), tc.newCount)
			if err != nil {
				t.Fatalf("record: %v", err)
			}
			if ok != tc.want {
				t.Fatalf("RecordAssertion(%d) = %v, want %v", tc.newCount, ok, tc.want)
			}
		})
	}

	// A successful assertion stamps last_used_at; a rejected one must not.
	rows, err := repo.ListCredentials(ctx, uid, "example.com")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.LastUsedAt == nil {
			t.Fatalf("credential %q was used but has no last_used_at", r.Nickname)
		}
	}
}

// Two concurrent assertions carrying the SAME counter cannot both succeed â€”
// the in-database compare-and-swap is what closes the replay race.
func TestWebAuthnRepo_ConcurrentAssertionRace(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	repo := NewWebAuthnRepo(s)
	uid := mkWebAuthnUser(t, s, "wa-arace@example.com")

	cred, err := repo.InsertCredential(ctx, uid, "example.com", []byte("c-race"), []byte(`{}`), 1, "race", nil)
	if err != nil {
		t.Fatal(err)
	}
	const n = 8
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		wins int
	)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := repo.RecordAssertion(ctx, cred.ID, []byte(`{}`), 2)
			if err == nil && ok {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("%d concurrent assertions with the same counter succeeded, want exactly 1", wins)
	}
}

// Deleting a user cascades to their passkeys and pending ceremonies.
func TestWebAuthnRepo_CascadesWithUser(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	repo := NewWebAuthnRepo(s)
	uid := mkWebAuthnUser(t, s, "wa-cascade@example.com")

	if _, err := repo.InsertCredential(ctx, uid, "example.com", []byte("c-cascade"), []byte(`{}`), 0, "gone soon", nil); err != nil {
		t.Fatal(err)
	}
	if err := repo.InsertChallenge(ctx, "chal-cascade", "login", &uid, []byte(`{}`), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetCredentialByCredentialID(ctx, []byte("c-cascade")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("credential survived the user: %v", err)
	}
	if _, err := repo.ClaimChallenge(ctx, "chal-cascade", "login"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("challenge survived the user: %v", err)
	}
}
