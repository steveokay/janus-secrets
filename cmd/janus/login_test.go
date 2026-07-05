package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoginStoresSession(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("JANUS_CONFIG_DIR", dir)
	t.Setenv("JANUS_ADDR", "")
	t.Setenv("JANUS_TOKEN", "")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "janus_session", Value: "sess-xyz", Path: "/"})
		_, _ = w.Write([]byte(`{"user":{"id":"u1","email":"me@corp.io"}}`))
	}))
	defer ts.Close()

	cmd := newLoginCmd()
	cmd.SetArgs([]string{"--address", ts.URL, "--email", "me@corp.io"})
	cmd.SetIn(strings.NewReader("hunter2\n")) // password from piped stdin
	cmd.SetOut(&strings.Builder{})
	cmd.SetErr(&strings.Builder{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	st, err := loadAuth()
	if err != nil {
		t.Fatal(err)
	}
	if st.Session != "sess-xyz" || st.Address != ts.URL || st.Email != "me@corp.io" {
		t.Fatalf("auth state after login: %+v", st)
	}
}
