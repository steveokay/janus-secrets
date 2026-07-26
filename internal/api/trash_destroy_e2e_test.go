package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/steveokay/janus-secrets/internal/store"
)

// Destroying from Trash was broken for configs AND environments: the delete
// handlers resolved the authz scope through a LIVE-ONLY repo read, but every
// row reachable from Trash is by definition already soft-deleted. The lookup
// 404'd before the authorization check ever ran, so the UI showed a Destroy
// button that could never succeed ("Action failed") — even for an owner.
//
// Projects were unaffected: handleProjectDelete builds authz.Resource{ProjectID}
// straight from the URL param and never reads the row, which is why the bug
// looked config-specific from the outside.
//
// These tests pin the whole trash surface — destroy AND restore, for all three
// kinds — so a future live-only resolver reintroduces a failing test rather than
// a dead button.
func TestTrashDestroySoftDeletedResources(t *testing.T) {
	ts, srv, email, password, _ := authStackFull(t)
	cookie := login(t, ts.URL, email, password)
	ctx := context.Background()

	newTree := func(slug string) (pid, eid, cid string) {
		t.Helper()
		p, err := srv.service.CreateProject(ctx, slug, slug)
		if err != nil {
			t.Fatal(err)
		}
		e, err := srv.service.CreateEnvironment(ctx, p.ID, "prod", "Prod")
		if err != nil {
			t.Fatal(err)
		}
		c, err := srv.service.CreateConfig(ctx, e.ID, "root", nil)
		if err != nil {
			t.Fatal(err)
		}
		return p.ID, e.ID, c.ID
	}

	t.Run("config with a live parent environment", func(t *testing.T) {
		_, _, cid := newTree("destroy-cfg-live-parent")
		store_ConfigSoftDelete(t, srv, cid)

		if code := doAuthed(t, "DELETE", ts.URL+"/v1/configs/"+cid+"?destroy=true",
			cookie, "", "", nil); code != http.StatusNoContent {
			t.Fatalf("destroy soft-deleted config: got %d, want 204", code)
		}
		// Truly gone, not merely re-flagged.
		if _, err := store.NewConfigRepo(srv.st).GetIncludingDeleted(ctx, cid); err == nil {
			t.Fatal("config row still present after destroy")
		}
	})

	// Soft-delete does not cascade, so a config and its environment are deleted
	// independently — this combination made resolveConfigScopeIncludingDeleted
	// 404 on the PARENT even after the config itself was read deleted-inclusively.
	t.Run("config whose parent environment is also soft-deleted", func(t *testing.T) {
		_, eid, cid := newTree("destroy-cfg-dead-parent")
		store_ConfigSoftDelete(t, srv, cid)
		store_EnvSoftDelete(t, srv, eid)

		if code := doAuthed(t, "DELETE", ts.URL+"/v1/configs/"+cid+"?destroy=true",
			cookie, "", "", nil); code != http.StatusNoContent {
			t.Fatalf("destroy config under a deleted env: got %d, want 204", code)
		}
	})

	t.Run("restore a config whose parent environment is soft-deleted", func(t *testing.T) {
		_, eid, cid := newTree("restore-cfg-dead-parent")
		store_ConfigSoftDelete(t, srv, cid)
		store_EnvSoftDelete(t, srv, eid)

		if code := doAuthed(t, "POST", ts.URL+"/v1/configs/"+cid+"/restore",
			cookie, "", "", nil); code != http.StatusOK {
			t.Fatalf("restore config under a deleted env: got %d, want 200", code)
		}
	})

	t.Run("environment", func(t *testing.T) {
		pid, eid, _ := newTree("destroy-env")
		store_EnvSoftDelete(t, srv, eid)

		if code := doAuthed(t, "DELETE", ts.URL+"/v1/projects/"+pid+"/environments/"+eid+"?destroy=true",
			cookie, "", "", nil); code != http.StatusNoContent {
			t.Fatalf("destroy soft-deleted environment: got %d, want 204", code)
		}
	})

	// Project destroy always worked; assert it still does so the fix to the
	// other two did not regress the one path that was fine.
	t.Run("project", func(t *testing.T) {
		pid, _, _ := newTree("destroy-proj")
		if err := store.NewProjectRepo(srv.st).SoftDelete(ctx, pid); err != nil {
			t.Fatal(err)
		}
		if code := doAuthed(t, "DELETE", ts.URL+"/v1/projects/"+pid+"?destroy=true",
			cookie, "", "", nil); code != http.StatusNoContent {
			t.Fatalf("destroy soft-deleted project: got %d, want 204", code)
		}
	})

	// Soft-deleting a LIVE config still works — the handler now resolves
	// deleted-inclusively for every call, not just the destroy ones.
	t.Run("soft-delete of a live config is unaffected", func(t *testing.T) {
		_, _, cid := newTree("soft-delete-live")
		if code := doAuthed(t, "DELETE", ts.URL+"/v1/configs/"+cid,
			cookie, "", "", nil); code != http.StatusNoContent {
			t.Fatalf("soft-delete live config: got %d, want 204", code)
		}
		c, err := store.NewConfigRepo(srv.st).GetIncludingDeleted(ctx, cid)
		if err != nil {
			t.Fatalf("row should still exist after a SOFT delete: %v", err)
		}
		if c.DeletedAt == nil {
			t.Fatal("soft delete did not set deleted_at")
		}
	})

	t.Run("a config that never existed is still 404", func(t *testing.T) {
		const ghost = "00000000-0000-0000-0000-000000000000"
		if code := doAuthed(t, "DELETE", ts.URL+"/v1/configs/"+ghost+"?destroy=true",
			cookie, "", "", nil); code != http.StatusNotFound {
			t.Fatalf("destroy nonexistent config: got %d, want 404", code)
		}
	})
}

// The fix makes soft-deleted rows FINDABLE; it must not make them destroyable by
// anyone who could not already destroy them. A viewer holds no delete permission
// and must still be refused — with 403, not the 404 the broken lookup produced,
// since conflating "not authorized" with "does not exist" is what hid this bug.
func TestTrashDestroyStillRequiresPermission(t *testing.T) {
	ts, srv, email, password, _ := authStackFull(t)
	_ = login(t, ts.URL, email, password)
	ctx := context.Background()

	p, err := srv.service.CreateProject(ctx, "destroy-authz", "Destroy Authz")
	if err != nil {
		t.Fatal(err)
	}
	e, err := srv.service.CreateEnvironment(ctx, p.ID, "prod", "Prod")
	if err != nil {
		t.Fatal(err)
	}
	c, err := srv.service.CreateConfig(ctx, e.ID, "root", nil)
	if err != nil {
		t.Fatal(err)
	}
	store_ConfigSoftDelete(t, srv, c.ID)
	store_EnvSoftDelete(t, srv, e.ID)

	viewerID, viewerPassword, err := srv.auth.CreateUser(ctx, "trash-viewer@corp.io")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.authz.Grant(ctx, store.RoleBindingInput{
		SubjectUserID: viewerID, ScopeLevel: "project", ProjectID: &p.ID, Role: "viewer",
	}); err != nil {
		t.Fatal(err)
	}
	viewerCookie := login(t, ts.URL, "trash-viewer@corp.io", viewerPassword)

	if code := doAuthed(t, "DELETE", ts.URL+"/v1/configs/"+c.ID+"?destroy=true",
		viewerCookie, "", "", nil); code != http.StatusForbidden {
		t.Fatalf("viewer destroying a config: got %d, want 403", code)
	}
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/projects/"+p.ID+"/environments/"+e.ID+"?destroy=true",
		viewerCookie, "", "", nil); code != http.StatusForbidden {
		t.Fatalf("viewer destroying an environment: got %d, want 403", code)
	}

	// And the row survived both refusals.
	if _, err := store.NewConfigRepo(srv.st).GetIncludingDeleted(ctx, c.ID); err != nil {
		t.Fatalf("config was destroyed despite a 403: %v", err)
	}
}
