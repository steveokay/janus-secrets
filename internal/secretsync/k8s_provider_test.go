package secretsync

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestK8sApplyServerSideApply(t *testing.T) {
	var (
		gotMethod, gotPath, gotRawQuery string
		gotContentType, gotAuth         string
		gotBody                         map[string]any
	)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotRawQuery = r.URL.RawQuery
		gotContentType = r.Header.Get("Content-Type")
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"kind":"Secret"}`))
	}))
	defer srv.Close()

	p := k8sProvider{newClient: func(string) (*http.Client, error) { return srv.Client(), nil }}
	res, err := p.Apply(context.Background(),
		Creds{APIURL: srv.URL, CACert: "", Token: "tok-1"},
		Addr{Namespace: "ns", SecretName: "app"},
		map[string]string{"A": "1", "B": "two"}, nil, true)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if gotMethod != http.MethodPatch {
		t.Errorf("method = %q, want PATCH", gotMethod)
	}
	if gotPath != "/api/v1/namespaces/ns/secrets/app" {
		t.Errorf("path = %q", gotPath)
	}
	if !strings.Contains(gotRawQuery, "fieldManager=janus") || !strings.Contains(gotRawQuery, "force=true") {
		t.Errorf("rawQuery = %q, want fieldManager=janus & force=true", gotRawQuery)
	}
	if gotContentType != "application/apply-patch+yaml" {
		t.Errorf("content-type = %q", gotContentType)
	}
	if gotAuth != "Bearer tok-1" {
		t.Errorf("authorization = %q", gotAuth)
	}

	data, ok := gotBody["data"].(map[string]any)
	if !ok {
		t.Fatalf("body.data missing/not a map: %v", gotBody)
	}
	if data["A"] != base64.StdEncoding.EncodeToString([]byte("1")) {
		t.Errorf("data[A] = %v, want base64(1)", data["A"])
	}
	if data["B"] != base64.StdEncoding.EncodeToString([]byte("two")) {
		t.Errorf("data[B] = %v, want base64(two)", data["B"])
	}

	if !containsStr(res.Applied, "A") || !containsStr(res.Applied, "B") {
		t.Errorf("Applied = %v, want A and B", res.Applied)
	}
}

func TestK8sPruneFalseMergePatch(t *testing.T) {
	var gotContentType, gotRawQuery string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		gotRawQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"kind":"Secret"}`))
	}))
	defer srv.Close()

	p := k8sProvider{newClient: func(string) (*http.Client, error) { return srv.Client(), nil }}
	_, err := p.Apply(context.Background(),
		Creds{APIURL: srv.URL, Token: "tok-1"},
		Addr{Namespace: "ns", SecretName: "app"},
		map[string]string{"A": "1"}, nil, false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if gotContentType != "application/merge-patch+json" {
		t.Errorf("content-type = %q, want application/merge-patch+json", gotContentType)
	}
	if strings.Contains(gotRawQuery, "fieldManager") {
		t.Errorf("rawQuery = %q, should not contain fieldManager", gotRawQuery)
	}
}

func TestK8sBadCACertRejected(t *testing.T) {
	_, err := defaultK8sClient("not a pem")
	if !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("err = %v, want ErrInvalidConfig", err)
	}
}

func TestK8sMissingConfig(t *testing.T) {
	p := k8sProvider{newClient: func(string) (*http.Client, error) {
		t.Fatal("client should not be built when config is invalid")
		return nil, nil
	}}
	cases := []struct {
		name  string
		creds Creds
		addr  Addr
	}{
		{"empty APIURL", Creds{Token: "t"}, Addr{Namespace: "ns", SecretName: "app"}},
		{"empty Token", Creds{APIURL: "https://x"}, Addr{Namespace: "ns", SecretName: "app"}},
		{"empty Namespace", Creds{APIURL: "https://x", Token: "t"}, Addr{SecretName: "app"}},
		{"empty SecretName", Creds{APIURL: "https://x", Token: "t"}, Addr{Namespace: "ns"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := p.Apply(context.Background(), tc.creds, tc.addr,
				map[string]string{"A": "1"}, nil, true)
			if !errors.Is(err, ErrInvalidConfig) {
				t.Errorf("err = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestK8sNon2xxIsError(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"kind":"Status","code":409}`))
	}))
	defer srv.Close()

	p := k8sProvider{newClient: func(string) (*http.Client, error) { return srv.Client(), nil }}
	_, err := p.Apply(context.Background(),
		Creds{APIURL: srv.URL, Token: "tok-1"},
		Addr{Namespace: "ns", SecretName: "app"},
		map[string]string{"A": "1"}, nil, true)
	if !errors.Is(err, ErrApplyFailed) {
		t.Errorf("err = %v, want ErrApplyFailed", err)
	}
}

func containsStr(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
