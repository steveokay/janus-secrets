package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/steveokay/janus-secrets/internal/auth"
	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/store"
)

// allowBindingStore is an in-memory authz.BindingStore that grants the subject
// instance owner — letting a handler-level test satisfy authorization without
// touching the DB (so we can close the pool and prove DB-degradation).
type allowBindingStore struct{ userID string }

func (a allowBindingStore) ListForUser(_ context.Context, userID string) ([]*store.RoleBinding, error) {
	return []*store.RoleBinding{{SubjectUserID: userID, ScopeLevel: "instance", Role: "owner"}}, nil
}
func (a allowBindingStore) ListForScope(context.Context, string, string) ([]*store.RoleBinding, error) {
	return nil, nil
}
func (a allowBindingStore) ListForScopePage(context.Context, string, string, int, *store.Cursor) ([]*store.RoleBinding, error) {
	return nil, nil
}
func (a allowBindingStore) Create(context.Context, store.RoleBindingInput) (*store.RoleBinding, error) {
	return nil, nil
}
func (a allowBindingStore) DeleteForSubjectScope(context.Context, string, string, *string, *string) error {
	return nil
}
func (a allowBindingStore) CountInstanceOwners(context.Context) (int, error) { return 1, nil }

// sysStatusWire mirrors sysStatusResponse for decode assertions in tests.
type sysStatusWire struct {
	Version       string `json:"version"`
	Commit        string `json:"commit"`
	UptimeSeconds int64  `json:"uptime_seconds"`
	Sealed        bool   `json:"sealed"`
	SealType      string `json:"seal_type"`
	DB            struct {
		Reachable bool  `json:"reachable"`
		LatencyMS int64 `json:"latency_ms"`
		Pool      struct {
			Total    int32 `json:"total"`
			Idle     int32 `json:"idle"`
			Acquired int32 `json:"acquired"`
			Max      int32 `json:"max"`
		} `json:"pool"`
	} `json:"db"`
	Audit struct {
		HeadSeq    int64 `json:"head_seq"`
		EventCount int64 `json:"event_count"`
	} `json:"audit"`
	Schedulers map[string]struct {
		Enabled            bool   `json:"enabled"`
		LastTickAgeSeconds *int64 `json:"last_tick_age_seconds"`
		IntervalSeconds    int64  `json:"interval_seconds"`
	} `json:"schedulers"`
	Runs struct {
		RotationFailed int64 `json:"rotation_failed"`
		SyncFailed     int64 `json:"sync_failed"`
	} `json:"runs"`
	Leases struct {
		Active int64 `json:"active"`
	} `json:"leases"`
}

// TestSysStatusAdminShape drives GET /v1/sys/status against the full stack: an
// audit-capable admin gets 200 with the expected shape; a config-scoped read
// token (no instance audit:read) is denied 403; unauthenticated is 401.
func TestSysStatusAdminShape(t *testing.T) {
	ts, _, email, password, cid := authStackFull(t)
	adminCookie := login(t, ts.URL, email, password)

	var st sysStatusWire
	if code := doAuthed(t, "GET", ts.URL+"/v1/sys/status", adminCookie, "", "", &st); code != 200 {
		t.Fatalf("status as admin: %d", code)
	}
	if st.SealType != "shamir" {
		t.Errorf("seal_type = %q, want shamir", st.SealType)
	}
	if st.Sealed {
		t.Errorf("sealed = true, want false (unsealed in authStackFull)")
	}
	if !st.DB.Reachable {
		t.Errorf("db.reachable = false, want true")
	}
	if st.DB.Pool.Max <= 0 {
		t.Errorf("db.pool.max = %d, want > 0", st.DB.Pool.Max)
	}
	// Every engine must be present with interval/enabled fields. In authStackFull
	// (BootConfig direct, zero ticks) schedulers are disabled.
	for _, eng := range []string{"rotation", "sync", "dynamic"} {
		sc, ok := st.Schedulers[eng]
		if !ok {
			t.Errorf("scheduler %q missing from status", eng)
			continue
		}
		if sc.Enabled {
			t.Errorf("scheduler %q enabled, want disabled (zero tick)", eng)
		}
	}

	// Config-scoped read token → no instance audit:read → 403.
	var minted struct {
		Token string `json:"token"`
	}
	mintBody := fmt.Sprintf(`{"name":"ro","scope":{"kind":"config","id":%q},"access":"read"}`, cid)
	if code := doAuthed(t, "POST", ts.URL+"/v1/tokens", adminCookie, "", mintBody, &minted); code != 200 || minted.Token == "" {
		t.Fatalf("mint ro token: %d", code)
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/sys/status", "", minted.Token, "", nil); code != 403 {
		t.Fatalf("status as config-scoped token: want 403, got %d", code)
	}

	// Unauthenticated → 401.
	if code := doAuthed(t, "GET", ts.URL+"/v1/sys/status", "", "", "", nil); code != 401 {
		t.Fatalf("status unauthenticated: want 401, got %d", code)
	}
}

// TestSysStatusDBUnreachableStill200 proves the handler's own DB reads degrade
// gracefully: with the store's pool closed (Ping + aggregate COUNTs all error),
// GET /v1/sys/status still returns 200 with db.reachable=false — the panel is
// never 500'd by a DB blip. Authorization is satisfied by an in-memory fake so
// the test targets the handler body's DB-degradation, not the (DB-backed) authz
// or session middleware (which legitimately fail on a fully-dead DB).
func TestSysStatusDBUnreachableStill200(t *testing.T) {
	ts, srv, _, _, _ := authStackFull(t)
	_ = ts

	// Baseline: with a live pool, reachable is true.
	baseReq := httptest.NewRequest("GET", "/v1/sys/status", nil)
	baseReq = baseReq.WithContext(withTestPrincipal(baseReq.Context()))
	srv.authz = authz.New(allowBindingStore{userID: "test-owner"})
	baseRec := httptest.NewRecorder()
	srv.handleSysStatus(baseRec, baseReq)
	if baseRec.Code != 200 {
		t.Fatalf("baseline: want 200, got %d", baseRec.Code)
	}
	var base sysStatusWire
	if err := json.Unmarshal(baseRec.Body.Bytes(), &base); err != nil {
		t.Fatalf("decode baseline: %v", err)
	}
	if !base.DB.Reachable {
		t.Fatalf("baseline db.reachable = false, want true")
	}

	// Kill the pool: the handler's Ping + COUNTs now error.
	srv.st.Close()

	downReq := httptest.NewRequest("GET", "/v1/sys/status", nil)
	downReq = downReq.WithContext(withTestPrincipal(downReq.Context()))
	downRec := httptest.NewRecorder()
	srv.handleSysStatus(downRec, downReq)
	if downRec.Code != 200 {
		t.Fatalf("status after DB down: want 200, got %d (body %s)", downRec.Code, downRec.Body.String())
	}
	var down sysStatusWire
	if err := json.Unmarshal(downRec.Body.Bytes(), &down); err != nil {
		t.Fatalf("decode down: %v", err)
	}
	if down.DB.Reachable {
		t.Errorf("db.reachable = true after pool close, want false")
	}
}

// withTestPrincipal injects a user principal into ctx so a handler-level test
// bypasses the RequireAuth middleware.
func withTestPrincipal(ctx context.Context) context.Context {
	return context.WithValue(ctx, principalCtxKey{},
		auth.Principal{Kind: auth.KindUser, ID: "test-owner", Name: "owner@test"})
}
