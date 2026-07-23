package auth

import (
	"context"
	"testing"
)

func TestMintTransitToken(t *testing.T) {
	svc, email, password := newTestService(t)
	ctx := context.Background()
	cookie, _ := svc.Login(ctx, email, []byte(password), "")
	by, _ := svc.VerifySession(ctx, cookie)

	// All-keys transit token (empty scopeID = all transit keys).
	raw, meta, err := svc.MintServiceToken(ctx, by, "ci", "transit", "", "use", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if meta.ScopeKind != "transit" || meta.Access != "use" || raw == "" {
		t.Fatalf("meta: %+v", meta)
	}
	_, scope, err := svc.VerifyServiceToken(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	if scope.Kind != "transit" || scope.ID != "" || scope.Access != "use" {
		t.Fatalf("scope: %+v", scope)
	}

	// Bad access for transit: read/readwrite are config/env accesses.
	if _, _, err := svc.MintServiceToken(ctx, by, "x", "transit", "", "readwrite", nil, nil); err == nil {
		t.Fatal("transit scope must reject access=readwrite")
	}
}
