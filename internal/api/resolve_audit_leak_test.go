package api

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/steveokay/janus-secrets/internal/store"
)

func TestResolveAuditTrailAndLeak(t *testing.T) {
	var logBuf syncBuffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	defer slog.SetDefault(prev)

	ts, srv, email, password, _ := authStackFull(t)
	cookie := login(t, ts.URL, email, password)
	const sentinel = "SENTINEL-REF-CANARY-8x2z"

	type idResp struct {
		ID string `json:"id"`
	}

	// billing/prod/api HOST=<sentinel>
	var billing, bEnv, bCfg idResp
	doAuthed(t, "POST", ts.URL+"/v1/projects", cookie, "", `{"slug":"billing","name":"B"}`, &billing)
	doAuthed(t, "POST", ts.URL+"/v1/projects/"+billing.ID+"/environments", cookie, "", `{"slug":"prod","name":"P"}`, &bEnv)
	doAuthed(t, "POST", ts.URL+"/v1/projects/"+billing.ID+"/environments/"+bEnv.ID+"/configs", cookie, "", `{"name":"api"}`, &bCfg)
	if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+bCfg.ID+"/secrets", cookie, "", `{"changes":[{"key":"HOST","value":"`+sentinel+`"}]}`, nil); code != 200 {
		t.Fatalf("seed billing: %d", code)
	}

	// app/prod/web URL=${projects.billing.prod.api.HOST}
	var app, aEnv, web idResp
	doAuthed(t, "POST", ts.URL+"/v1/projects", cookie, "", `{"slug":"app","name":"A"}`, &app)
	doAuthed(t, "POST", ts.URL+"/v1/projects/"+app.ID+"/environments", cookie, "", `{"slug":"prod","name":"P"}`, &aEnv)
	doAuthed(t, "POST", ts.URL+"/v1/projects/"+app.ID+"/environments/"+aEnv.ID+"/configs", cookie, "", `{"name":"web"}`, &web)
	if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+web.ID+"/secrets", cookie, "", `{"changes":[{"key":"URL","value":"${projects.billing.prod.api.HOST}"}]}`, nil); code != 200 {
		t.Fatalf("seed web: %d", code)
	}

	// Successful resolved reveal → 200; URL resolves to the sentinel (flows to client, expected).
	var got struct {
		Secrets map[string]string `json:"secrets"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+web.ID+"/secrets?reveal=true", cookie, "", "", &got); code != 200 {
		t.Fatalf("resolved reveal: %d", code)
	}
	if got.Secrets["URL"] != sentinel {
		t.Fatalf("URL resolved = %q, want the sentinel", got.Secrets["URL"])
	}

	// (a) Audit trail: primary reveal on web + per-deref reveal on billing.
	var sawPrimary, sawDeref bool
	if err := store.NewAuditRepo(srv.st).Iterate(context.Background(), func(a store.AuditRow) error {
		if a.Action != "secret.reveal" {
			return nil
		}
		if a.Resource == "configs/"+web.ID+"/secrets" {
			sawPrimary = true
		}
		if a.Resource == "configs/"+bCfg.ID+"/secrets" && strings.Contains(derefStr(a.Detail), "via reference") {
			sawDeref = true
		}
		return nil
	}); err != nil {
		t.Fatalf("iterate audit: %v", err)
	}
	if !sawPrimary || !sawDeref {
		t.Fatalf("audit trail: primary=%v deref=%v", sawPrimary, sawDeref)
	}

	// (b) No leak on success: the request logger never writes the sentinel value.
	if strings.Contains(logBuf.String(), sentinel) {
		t.Fatal("sentinel value leaked into captured logs")
	}
	if !strings.Contains(logBuf.String(), "/v1/configs/"+web.ID+"/secrets") {
		t.Fatalf("expected request logs to include the reveal path")
	}

	// (c) Failed resolution (reference to a missing key) → 422, no sentinel in body or logs.
	var webBad idResp
	doAuthed(t, "POST", ts.URL+"/v1/projects/"+app.ID+"/environments/"+aEnv.ID+"/configs", cookie, "", `{"name":"webbad"}`, &webBad)
	if code := doAuthed(t, "PUT", ts.URL+"/v1/configs/"+webBad.ID+"/secrets", cookie, "", `{"changes":[{"key":"X","value":"${projects.billing.prod.api.NOSUCH}"}]}`, nil); code != 200 {
		t.Fatalf("seed webbad: %d", code)
	}
	code, body := rawGet(t, ts.URL+"/v1/configs/"+webBad.ID+"/secrets?reveal=true", cookie)
	if code != 422 {
		t.Fatalf("failed resolve: want 422, got %d (body %s)", code, body)
	}
	if strings.Contains(body, sentinel) {
		t.Fatalf("sentinel leaked into error body: %s", body)
	}
	if strings.Contains(logBuf.String(), sentinel) {
		t.Fatal("sentinel leaked into logs after failed resolve")
	}
}
