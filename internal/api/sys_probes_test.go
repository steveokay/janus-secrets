package api

import "testing"

func TestLiveAlways200(t *testing.T) {
	_, ts, _ := newShamirTestServer(t)
	var body struct{ Status string }
	if code := doJSON(t, "GET", ts.URL+"/v1/sys/live", "", &body); code != 200 || body.Status != "live" {
		t.Fatalf("live = %d %+v", code, body)
	}
}

func TestReadyUninitialized503(t *testing.T) {
	_, ts, _ := newShamirTestServer(t)
	var env errEnvelope
	if code := doJSON(t, "GET", ts.URL+"/v1/sys/ready", "", &env); code != 503 || env.Error.Code != CodeNotInitialized {
		t.Fatalf("ready = %d %+v (want 503 not_initialized)", code, env)
	}
}

func TestReadySealed503(t *testing.T) {
	srv, ts, _ := newShamirTestServer(t)
	// Initialize but do not unseal: init via the endpoint, then reseal.
	var ir struct{ Shares []string }
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/init", `{"shares":1,"threshold":1}`, &ir); code != 200 {
		t.Fatalf("init: %d", code)
	}
	srv.keyring.Seal()
	var env errEnvelope
	if code := doJSON(t, "GET", ts.URL+"/v1/sys/ready", "", &env); code != 503 || env.Error.Code != CodeSealed {
		t.Fatalf("ready = %d %+v (want 503 sealed)", code, env)
	}
}

func TestReadyE2E(t *testing.T) {
	ts, _, _, _, _ := authStackFull(t) // initialized + unsealed + live DB
	var body struct{ Status string }
	if code := doJSON(t, "GET", ts.URL+"/v1/sys/ready", "", &body); code != 200 || body.Status != "ready" {
		t.Fatalf("ready = %d %+v", code, body)
	}
}
