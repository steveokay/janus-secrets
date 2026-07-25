// Package notification delivers outbound alerts (generic webhook, Slack) by
// tailing the value-free audit log and fanning matching events out to
// configured channels through a crash-safe delivery outbox. Because the source
// is the audit log — which has no value field by construction — a notification
// can never carry a secret value.
package notification

import (
	"slices"
	"time"

	"github.com/steveokay/janus-secrets/internal/store"
)

// Event kinds a channel can subscribe to. These are stable, normalized names
// derived from (audit action, result); the raw audit action strings are an
// internal detail.
const (
	EventRotationFailed = "rotation.failed"
	EventSyncFailed     = "sync.failed"
	// EventSyncDrift fires when a sync drift-verification pass finds the external
	// destination out of step with Janus (or could not complete). Derived from
	// the value-free sync.verify audit event, so it carries counts only.
	EventSyncDrift           = "sync.drift"
	EventPromotionPending    = "promotion.pending"
	EventAccessDenied        = "access.denied"
	EventBreakGlassActivated = "breakglass.activated"
)

// KnownEventKinds is the set a channel may subscribe to (validated on write).
var KnownEventKinds = []string{
	EventRotationFailed, EventSyncFailed, EventSyncDrift, EventPromotionPending,
	EventAccessDenied, EventBreakGlassActivated,
}

func isKnownKind(k string) bool { return slices.Contains(KnownEventKinds, k) }

// classify maps an audit row to a notification event kind, or "" if the event
// is not notifiable. A single denial catch-all (any action with result
// "denied") plus the specific failure/pending signals.
func classify(a store.AuditRow) string {
	switch {
	// Break-glass activation is the loudest event in the system: forward every
	// successful elevation so humans are alerted immediately. Checked before the
	// denied catch-all (an activation is a success, so order is moot, but keep it
	// first for prominence).
	case a.Action == "breakglass.activate" && a.Result == "success":
		return EventBreakGlassActivated
	case a.Result == "denied":
		return EventAccessDenied
	case a.Action == "rotation.rotate" && a.Result == "failure":
		return EventRotationFailed
	case a.Action == "sync.reconcile" && a.Result == "failure":
		return EventSyncFailed
	// Drift verification (roadmap 7.4): the engine records result "failure" when
	// the destination drifted OR the pass errored; Detail carries the value-free
	// counts (status=… missing=… modified=… extra=…).
	case a.Action == "sync.verify" && a.Result == "failure":
		return EventSyncDrift
	case a.Action == "promotion.request.create" && a.Result == "success":
		return EventPromotionPending
	default:
		return ""
	}
}

// eventPayload is the value-free JSON body enqueued for a matched event. It
// mirrors the audit row's non-secret fields only (audit has no value field).
type eventPayload struct {
	Event      string    `json:"event"`
	Seq        int64     `json:"seq"`
	OccurredAt time.Time `json:"occurred_at"`
	Action     string    `json:"action"`
	Result     string    `json:"result"`
	Resource   string    `json:"resource"`
	Actor      string    `json:"actor"`
	Detail     string    `json:"detail,omitempty"`
}

func payloadFor(kind string, a store.AuditRow) eventPayload {
	p := eventPayload{
		Event: kind, Seq: a.Seq, OccurredAt: a.OccurredAt.UTC(),
		Action: a.Action, Result: a.Result, Resource: a.Resource, Actor: a.ActorName,
	}
	if a.Detail != nil {
		p.Detail = *a.Detail
	}
	return p
}
