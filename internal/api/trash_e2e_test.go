package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
)

type trashResp struct {
	Projects []struct {
		ID        string `json:"id"`
		Slug      string `json:"slug"`
		Name      string `json:"name"`
		DeletedAt string `json:"deleted_at"`
	} `json:"projects"`
	Environments []struct {
		ID          string `json:"id"`
		Slug        string `json:"slug"`
		Name        string `json:"name"`
		ProjectID   string `json:"project_id"`
		ProjectName string `json:"project_name"`
		DeletedAt   string `json:"deleted_at"`
	} `json:"environments"`
	Configs []struct {
		ID              string `json:"id"`
		Name            string `json:"name"`
		EnvironmentID   string `json:"environment_id"`
		EnvironmentName string `json:"environment_name"`
		ProjectID       string `json:"project_id"`
		ProjectName     string `json:"project_name"`
		DeletedAt       string `json:"deleted_at"`
	} `json:"configs"`
}

// seedSecret persists a secret into a config via the real secrets service.
func seedSecret(t *testing.T, srv *Server, cid, key, value string) {
	t.Helper()
	if _, err := srv.service.SetSecrets(context.Background(), cid, []secrets.SecretChange{
		{Key: key, Value: []byte(value)},
	}, "seed", "test"); err != nil {
		t.Fatal(err)
	}
}

func store_ConfigSoftDelete(t *testing.T, srv *Server, cid string) {
	t.Helper()
	if err := store.NewConfigRepo(srv.st).SoftDelete(context.Background(), cid); err != nil {
		t.Fatal(err)
	}
}
func store_EnvSoftDelete(t *testing.T, srv *Server, eid string) {
	t.Helper()
	if err := store.NewEnvironmentRepo(srv.st).SoftDelete(context.Background(), eid); err != nil {
		t.Fatal(err)
	}
}
func TestTrashListGroupsDeletedWithLabels(t *testing.T) {
	ts, srv, email, password, _ := authStackFull(t)
	cookie := login(t, ts.URL, email, password)
	ctx := context.Background()

	p, err := srv.service.CreateProject(ctx, "web", "Web")
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
	seedSecret(t, srv, c.ID, "DB_PASSWORD", "redacted-fixture-a")

	store_ConfigSoftDelete(t, srv, c.ID)
	store_EnvSoftDelete(t, srv, e.ID)

	var got trashResp
	if code := doAuthed(t, "GET", ts.URL+"/v1/trash", cookie, "", "", &got); code != http.StatusOK {
		t.Fatalf("GET /v1/trash: %d", code)
	}
	if len(got.Configs) != 1 || got.Configs[0].ID != c.ID {
		t.Fatalf("want config %s in trash, got %+v", c.ID, got.Configs)
	}
	if got.Configs[0].EnvironmentName != "Prod" || got.Configs[0].ProjectName != "Web" {
		t.Fatalf("want parent labels Prod/Web, got %+v", got.Configs[0])
	}
	if len(got.Environments) != 1 || got.Environments[0].ProjectName != "Web" {
		t.Fatalf("want env with project label Web, got %+v", got.Environments)
	}
}

func TestTrashListValueFree(t *testing.T) {
	ts, srv, email, password, _ := authStackFull(t)
	cookie := login(t, ts.URL, email, password)
	ctx := context.Background()
	p, _ := srv.service.CreateProject(ctx, "leaktest", "Leak Test")
	e, _ := srv.service.CreateEnvironment(ctx, p.ID, "prod", "Prod")
	c, _ := srv.service.CreateConfig(ctx, e.ID, "root", nil)
	const secret = "redacted-fixture-b"
	seedSecret(t, srv, c.ID, "API_KEY", secret)
	store_ConfigSoftDelete(t, srv, c.ID)

	body := doAuthedRaw(t, "GET", ts.URL+"/v1/trash", cookie).body
	if body == "" {
		t.Fatal("empty body")
	}
	if strings.Contains(body, secret) {
		t.Fatalf("secret value leaked into /v1/trash body")
	}
}
