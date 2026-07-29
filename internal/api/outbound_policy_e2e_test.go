package api

import (
	"fmt"
	"testing"

	"github.com/steveokay/janus-secrets/internal/nethard"
)

type outboundPolicyWire struct {
	BlockPrivate  bool     `json:"block_private"`
	Allow         []string `json:"allow"`
	AllowProxy    bool     `json:"allow_proxy"`
	Source        string   `json:"source"`
	Locked        bool     `json:"locked"`
	UpdatedAt     *string  `json:"updated_at"`
	UpdatedBy     *string  `json:"updated_by"`
	AlwaysBlocked []string `json:"always_blocked"`
}

// TestOutboundPolicyRoundTrip covers the ordinary path: an owner stores a
// policy, it reads back normalised, it reports as stored rather than
// environment, and resetting returns the instance to the environment.
func TestOutboundPolicyRoundTrip(t *testing.T) {
	ts, _, email, password, _ := authStackFull(t)
	owner := login(t, ts.URL, email, password)

	var got outboundPolicyWire
	if code := doAuthed(t, "GET", ts.URL+"/v1/sys/outbound-policy", owner, "", "", &got); code != 200 {
		t.Fatalf("initial get: %d", code)
	}
	if got.Source != "environment" {
		t.Errorf("source = %q, want environment before any write", got.Source)
	}
	if len(got.AlwaysBlocked) == 0 {
		t.Error("always_blocked must be reported so the screen can state the guarantee")
	}

	// Host bits are normalised away by the same parser the guard uses.
	body := `{"block_private":true,"allow":["10.96.0.1","10.0.0.1/8"]}`
	if code := doAuthed(t, "PUT", ts.URL+"/v1/sys/outbound-policy", owner, "", body, &got); code != 200 {
		t.Fatalf("put: %d", code)
	}
	if !got.BlockPrivate {
		t.Error("block_private did not persist")
	}
	want := []string{"10.96.0.1/32", "10.0.0.0/8"}
	if fmt.Sprint(got.Allow) != fmt.Sprint(want) {
		t.Errorf("allow = %v, want %v (normalised)", got.Allow, want)
	}
	if got.Source != "stored" {
		t.Errorf("source = %q, want stored", got.Source)
	}
	if got.UpdatedAt == nil || got.UpdatedBy == nil {
		t.Error("provenance must be reported so the UI can say who changed egress policy")
	}

	// The edit must reach the live process source, not just the database —
	// that is the entire point of storing it.
	live := nethard.Process().Policy()
	if !live.BlockPrivate || len(live.Allow) != 2 {
		t.Fatalf("live policy did not follow the write: %+v", live)
	}

	if code := doAuthed(t, "DELETE", ts.URL+"/v1/sys/outbound-policy", owner, "", "", &got); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	if got.Source != "environment" {
		t.Errorf("source = %q, want environment after reset", got.Source)
	}
}

// TestOutboundPolicyRejects pins the refusals that keep the control meaningful.
func TestOutboundPolicyRejects(t *testing.T) {
	ts, _, email, password, _ := authStackFull(t)
	owner := login(t, ts.URL, email, password)
	t.Cleanup(func() {
		doAuthed(t, "DELETE", ts.URL+"/v1/sys/outbound-policy", owner, "", "", nil)
	})

	tests := []struct {
		name string
		body string
	}{
		// The whole security argument: no stored policy may name a range the
		// guard blocks unconditionally.
		{"metadata address", `{"block_private":true,"allow":["169.254.169.254/32"]}`},
		{"link-local range", `{"block_private":true,"allow":["fe80::/10"]}`},
		// Hostnames would mean trusting DNS, reopening rebinding.
		{"hostname", `{"block_private":true,"allow":["kubernetes.default.svc"]}`},
		{"garbage", `{"block_private":true,"allow":["not-an-ip"]}`},
		// allow_proxy is env-only; accepting-then-ignoring would mislead.
		{"allow_proxy", `{"block_private":true,"allow":[],"allow_proxy":true}`},
		{"missing fields", `{"block_private":true}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if code := doAuthed(t, "PUT", ts.URL+"/v1/sys/outbound-policy", owner, "", tt.body, nil); code != 400 {
				t.Fatalf("expected 400, got %d", code)
			}
		})
	}
}

// TestOutboundPolicyRequiresOwner pins that egress policy is owner-only. It is
// deliberately NOT an admin capability: configuring integrations is already an
// admin privilege, so letting admin also edit the guard would put the control
// under the authority it exists to bound.
func TestOutboundPolicyRequiresOwner(t *testing.T) {
	ts, _, email, password, cid := authStackFull(t)
	owner := login(t, ts.URL, email, password)

	var minted struct {
		Token string `json:"token"`
	}
	mintBody := fmt.Sprintf(`{"name":"ro","scope":{"kind":"config","id":%q},"access":"read"}`, cid)
	if code := doAuthed(t, "POST", ts.URL+"/v1/tokens", owner, "", mintBody, &minted); code != 200 {
		t.Fatalf("mint: %d", code)
	}
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		if code := doAuthed(t, method, ts.URL+"/v1/sys/outbound-policy", "", minted.Token, `{"block_private":false,"allow":[]}`, nil); code != 403 {
			t.Errorf("%s as a config-scoped token: want 403, got %d", method, code)
		}
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/sys/outbound-policy", "", "", "", nil); code != 401 {
		t.Errorf("unauthenticated: want 401, got %d", code)
	}
}
