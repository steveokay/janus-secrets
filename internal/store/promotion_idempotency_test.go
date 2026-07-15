package store

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestPromotionIdempotencyClaimCompleteRelease(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	repo := NewPromotionIdempotencyRepo(s)

	const key, actor, hash = "k1", "actor-1", "abc123"

	// First claim wins.
	claimed, existing, err := repo.Claim(ctx, key, actor, hash)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if !claimed || existing != nil {
		t.Fatalf("first claim: want claimed=true existing=nil, got claimed=%v existing=%v", claimed, existing)
	}

	// Second claim (same hash) loses; row is pending (Response==nil).
	claimed, existing, err = repo.Claim(ctx, key, actor, hash)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if claimed {
		t.Fatalf("second claim: want claimed=false, got true")
	}
	if existing == nil {
		t.Fatalf("second claim: want existing record, got nil")
	}
	if existing.RequestHash != hash {
		t.Fatalf("second claim: want request_hash %q, got %q", hash, existing.RequestHash)
	}
	if existing.Response != nil {
		t.Fatalf("second claim: want pending (Response==nil), got %q", existing.Response)
	}

	// Complete stores the response.
	resp := []byte(`{"target_version":3,"applied":["B"]}`)
	if err := repo.Complete(ctx, key, actor, resp); err != nil {
		t.Fatalf("complete: %v", err)
	}

	// Claim again now sees the stored response.
	claimed, existing, err = repo.Claim(ctx, key, actor, hash)
	if err != nil {
		t.Fatalf("third claim: %v", err)
	}
	if claimed {
		t.Fatalf("third claim: want claimed=false, got true")
	}
	if existing == nil || existing.Response == nil {
		t.Fatalf("third claim: want a stored response, got %v", existing)
	}
	// response is jsonb: Postgres may reorder keys / normalize whitespace, so
	// compare semantically rather than byte-for-byte.
	var want, got map[string]any
	if err := json.Unmarshal(resp, &want); err != nil {
		t.Fatalf("unmarshal want: %v", err)
	}
	if err := json.Unmarshal(existing.Response, &got); err != nil {
		t.Fatalf("unmarshal got: %v", err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("third claim: want response %v, got %v", want, got)
	}

	// Release then Claim succeeds again (proves the claim is gone).
	if err := repo.Release(ctx, key, actor); err != nil {
		t.Fatalf("release: %v", err)
	}
	claimed, existing, err = repo.Claim(ctx, key, actor, hash)
	if err != nil {
		t.Fatalf("post-release claim: %v", err)
	}
	if !claimed || existing != nil {
		t.Fatalf("post-release claim: want claimed=true existing=nil, got claimed=%v existing=%v", claimed, existing)
	}
}
