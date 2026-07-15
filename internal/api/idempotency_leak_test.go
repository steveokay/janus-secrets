package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestIdempotency_TokenNotPersisted is a security regression test proving the
// generic idempotency table is value-free. The Idempotency-Key middleware
// captures ONLY the final HTTP status of a mutating request — never the response
// body — so a once-shown secret (here, the plaintext service token returned by
// POST /v1/tokens) can never persist in the table.
//
// The test mints a token WITH an Idempotency-Key header (so the middleware
// engages and writes a row), captures the once-shown plaintext token from the
// response, then queries every text column of every idempotency row directly and
// asserts the token substring appears in NONE of them. It also asserts a row was
// actually written, so the check cannot pass vacuously.
func TestIdempotency_TokenNotPersisted(t *testing.T) {
	ts, _, email, password, configID, dsn := authStackFullDSN(t)
	ctx := context.Background()
	cookie := login(t, ts.URL, email, password)

	// Mint a config-scoped read token WITH an Idempotency-Key. This engages the
	// middleware (a claim row is written) and returns the plaintext token once.
	body := fmt.Sprintf(`{"name":"ci","scope":{"kind":"config","id":%q},"access":"read","ttl_seconds":3600}`, configID)
	req, err := http.NewRequest("POST", ts.URL+"/v1/tokens", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	req.Header.Set("Idempotency-Key", "leak-key-1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var minted struct {
		Token string `json:"token"`
		ID    string `json:"id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&minted)
	resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		t.Fatalf("mint with idempotency key: want 200/201, got %d", resp.StatusCode)
	}

	// Guard against a false pass: the captured token must be a real, non-empty
	// plaintext service token.
	if minted.Token == "" || !strings.HasPrefix(minted.Token, "janus_") {
		t.Fatalf("captured token is not a plaintext janus_ token: %q", minted.Token)
	}

	// Open a direct pool to the same DB and read EVERY idempotency row.
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("open verification pool: %v", err)
	}
	defer pool.Close()

	rows, err := pool.Query(ctx,
		`SELECT idempotency_key, actor, endpoint, request_hash, status_code FROM idempotency`)
	if err != nil {
		t.Fatalf("query idempotency: %v", err)
	}
	defer rows.Close()

	seen := 0
	for rows.Next() {
		seen++
		var key, actor, endpoint, hash string
		var status int
		if err := rows.Scan(&key, &actor, &endpoint, &hash, &status); err != nil {
			t.Fatalf("scan idempotency row: %v", err)
		}
		for col, val := range map[string]string{
			"idempotency_key": key,
			"actor":           actor,
			"endpoint":        endpoint,
			"request_hash":    hash,
		} {
			if strings.Contains(val, minted.Token) {
				t.Fatalf("plaintext token leaked into idempotency.%s: %q", col, val)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate idempotency rows: %v", err)
	}

	// The keyed mint must have produced exactly the middleware's row; a zero count
	// would mean the middleware never engaged and the leak check was vacuous.
	if seen == 0 {
		t.Fatal("no idempotency row was written — middleware did not engage, leak check is vacuous")
	}
}
