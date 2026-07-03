package store

import (
	"context"
	"sync"
	"testing"
)

func TestConcurrentSavesProduceContiguousVersions(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	_, _, configID := mkConfig(t, s, "prod")
	repo := NewSecretRepo(s)

	const n = 10
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = repo.SaveConfigVersion(ctx, configID,
				[]Change{{Key: "K", Encrypt: set("v")}}, "concurrent", "u")
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}

	versions, err := repo.ListVersions(ctx, configID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != n {
		t.Fatalf("versions = %d, want %d", len(versions), n)
	}
	for i, cv := range versions {
		if cv.Version != i+1 {
			t.Fatalf("version[%d] = %d, want %d (not contiguous)", i, cv.Version, i+1)
		}
	}
}
