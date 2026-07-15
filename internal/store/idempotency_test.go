package store

import (
	"context"
	"testing"
)

func TestIdempotencyRepo_ClaimCompleteReplayRelease(t *testing.T) {
	st := requireStore(t)
	resetDB(t)
	repo := NewIdempotencyRepo(st)
	ctx := context.Background()
	const key, actor, ep, hash = "k1", "actor-1", "POST /v1/projects", "hash-abc"

	claimed, existing, err := repo.Claim(ctx, key, actor, ep, hash)
	if err != nil || !claimed || existing != nil {
		t.Fatalf("first claim: claimed=%v existing=%v err=%v", claimed, existing, err)
	}
	claimed, existing, err = repo.Claim(ctx, key, actor, ep, hash)
	if err != nil || claimed || existing == nil || existing.StatusCode != 0 {
		t.Fatalf("second claim: claimed=%v existing=%+v err=%v", claimed, existing, err)
	}
	if err := repo.Complete(ctx, key, actor, 200); err != nil {
		t.Fatal(err)
	}
	claimed, existing, err = repo.Claim(ctx, key, actor, ep, hash)
	if err != nil || claimed || existing.StatusCode != 200 {
		t.Fatalf("after complete: %+v err=%v", existing, err)
	}
	if err := repo.Release(ctx, key, actor); err != nil {
		t.Fatal(err)
	}
	claimed, _, err = repo.Claim(ctx, key, actor, ep, hash)
	if err != nil || !claimed {
		t.Fatalf("after release, re-claim should win: claimed=%v err=%v", claimed, err)
	}
	claimed, _, err = repo.Claim(ctx, key, "actor-2", ep, hash)
	if err != nil || !claimed {
		t.Fatalf("different actor: claimed=%v err=%v", claimed, err)
	}
}
