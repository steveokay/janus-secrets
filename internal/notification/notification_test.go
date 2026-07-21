package notification

import (
	"testing"
	"time"

	"github.com/steveokay/janus-secrets/internal/store"
)

func TestClassify(t *testing.T) {
	det := func(s string) *string { return &s }
	cases := []struct {
		name string
		row  store.AuditRow
		want string
	}{
		{"denied", store.AuditRow{Action: "notification.channels.list", Result: "denied"}, EventAccessDenied},
		{"denied wins over action", store.AuditRow{Action: "rotation.rotate", Result: "denied"}, EventAccessDenied},
		{"rotation failure", store.AuditRow{Action: "rotation.rotate", Result: "failure", Detail: det("apply failed")}, EventRotationFailed},
		{"rotation success ignored", store.AuditRow{Action: "rotation.rotate", Result: "success"}, ""},
		{"sync failure", store.AuditRow{Action: "sync.reconcile", Result: "failure"}, EventSyncFailed},
		{"promotion pending", store.AuditRow{Action: "promotion.request.create", Result: "success"}, EventPromotionPending},
		{"login success ignored", store.AuditRow{Action: "auth.login", Result: "success"}, ""},
		{"reveal ignored", store.AuditRow{Action: "secret.reveal", Result: "success"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classify(c.row); got != c.want {
				t.Fatalf("classify = %q, want %q", got, c.want)
			}
		})
	}
}

func TestPayloadIsValueFree(t *testing.T) {
	det := func(s string) *string { return &s }
	row := store.AuditRow{
		Seq: 7, Action: "rotation.rotate", Result: "failure", Resource: "configs/x/secrets/STRIPE_KEY",
		ActorName: "rotation:policy-1", Detail: det("apply failed"), OccurredAt: time.Unix(1000, 0),
	}
	p := payloadFor(EventRotationFailed, row)
	// The payload mirrors only audit metadata — audit has no value field, so this
	// is structurally value-free. Assert the shape carries the name/path, not more.
	if p.Resource != "configs/x/secrets/STRIPE_KEY" || p.Detail != "apply failed" || p.Actor != "rotation:policy-1" {
		t.Fatalf("payload lost metadata: %+v", p)
	}
	if p.Event != EventRotationFailed || p.Seq != 7 {
		t.Fatalf("payload header wrong: %+v", p)
	}
}

func TestBackoff(t *testing.T) {
	// 1m, 2m, 4m, 8m, 16m, 32m, then capped at 1h.
	want := []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 16 * time.Minute, 32 * time.Minute, time.Hour, time.Hour}
	for i, w := range want {
		if got := backoff(i + 1); got != w {
			t.Fatalf("backoff(%d) = %s, want %s", i+1, got, w)
		}
	}
}

func TestValidateChannel(t *testing.T) {
	if err := validateChannel("email", []string{EventSyncFailed}, "https://x/y"); err == nil {
		t.Fatal("bad type should fail")
	}
	if err := validateChannel("webhook", nil, "https://x/y"); err == nil {
		t.Fatal("no events should fail")
	}
	if err := validateChannel("webhook", []string{"bogus.kind"}, "https://x/y"); err == nil {
		t.Fatal("unknown event kind should fail")
	}
	if err := validateChannel("webhook", []string{EventSyncFailed}, "ftp://x/y"); err == nil {
		t.Fatal("non-http url should fail")
	}
	if err := validateChannel("slack", []string{EventAccessDenied}, "https://hooks.slack.com/x"); err != nil {
		t.Fatalf("valid slack channel rejected: %v", err)
	}
}
