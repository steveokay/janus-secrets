package api

import "testing"

func TestHealthAlways200(t *testing.T) {
	_, ts, _ := newShamirTestServer(t)
	var body struct {
		Status      string `json:"status"`
		Initialized bool   `json:"initialized"`
		Sealed      bool   `json:"sealed"`
	}
	if code := doJSON(t, "GET", ts.URL+"/v1/sys/health", "", &body); code != 200 {
		t.Fatalf("health status = %d", code)
	}
	if body.Status != "ok" || body.Initialized || !body.Sealed {
		t.Fatalf("health body = %+v (want ok, uninitialized, sealed)", body)
	}
}

func TestSealStatusUninitialized(t *testing.T) {
	_, ts, _ := newShamirTestServer(t)
	var body struct {
		Initialized bool   `json:"initialized"`
		Sealed      bool   `json:"sealed"`
		Type        string `json:"type"`
	}
	if code := doJSON(t, "GET", ts.URL+"/v1/sys/seal-status", "", &body); code != 200 {
		t.Fatalf("status = %d", code)
	}
	if body.Initialized || !body.Sealed || body.Type != "shamir" {
		t.Fatalf("body = %+v", body)
	}
}
