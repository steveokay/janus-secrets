package authz

import (
	"context"
	"testing"

	"github.com/steveokay/janus-secrets/internal/auth"
)

func TestTransitTokenCapabilities(t *testing.T) {
	e := New(nil) // token path doesn't touch the binding store
	tok := auth.Principal{Kind: auth.KindServiceToken, ID: "t1"}

	useAll := &TokenScope{Kind: "transit", ID: "", Access: "use"}
	if err := e.Can(context.Background(), tok, useAll, TransitUse, Resource{TransitKey: "any"}); err != nil {
		t.Fatalf("use token should allow transit:use on any key: %v", err)
	}
	if err := e.Can(context.Background(), tok, useAll, TransitManage, Resource{TransitKey: "any"}); err == nil {
		t.Fatal("use token must NOT allow transit:manage")
	}
	if err := e.Can(context.Background(), tok, useAll, SecretRead, Resource{ConfigID: "c1"}); err == nil {
		t.Fatal("transit token must NOT allow secret:read")
	}

	scoped := &TokenScope{Kind: "transit", ID: "billing", Access: "manage"}
	if err := e.Can(context.Background(), tok, scoped, TransitManage, Resource{TransitKey: "billing"}); err != nil {
		t.Fatalf("manage token should allow its key: %v", err)
	}
	if err := e.Can(context.Background(), tok, scoped, TransitUse, Resource{TransitKey: "other"}); err == nil {
		t.Fatal("key-restricted token must deny a different key")
	}

	// Unknown access → nil capabilities (covers the default branch).
	bogus := &TokenScope{Kind: "transit", ID: "", Access: "bogus"}
	if err := e.Can(context.Background(), tok, bogus, TransitUse, Resource{TransitKey: "any"}); err == nil {
		t.Fatal("transit token with unknown access must deny")
	}
}

func TestTransitRoleMatrix(t *testing.T) {
	if !roleAllows(RoleViewer, TransitRead) {
		t.Fatal("viewer reads transit metadata")
	}
	if roleAllows(RoleViewer, TransitUse) {
		t.Fatal("viewer must not use transit")
	}
	if !roleAllows(RoleDeveloper, TransitUse) || roleAllows(RoleDeveloper, TransitManage) {
		t.Fatal("developer uses but does not manage transit")
	}
	if !roleAllows(RoleAdmin, TransitManage) {
		t.Fatal("admin manages transit")
	}
}
