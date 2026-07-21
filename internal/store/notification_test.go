package store

import (
	"context"
	"testing"
	"time"
)

func TestNotificationRepo(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	repo := NewNotificationRepo(s)

	id, err := s.NewID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := repo.CreateChannel(ctx, &NotificationChannel{
		ID: id, Name: "alerts", Type: "webhook", Enabled: true,
		Events: []string{"access.denied", "sync.failed"}, ConfigCT: []byte("wrapped-blob"), CreatedBy: "root",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ch.Name != "alerts" || len(ch.Events) != 2 {
		t.Fatalf("channel round-trip wrong: %+v", ch)
	}

	// Name is unique.
	id2, _ := s.NewID(ctx)
	if _, err := repo.CreateChannel(ctx, &NotificationChannel{
		ID: id2, Name: "alerts", Type: "slack", Events: []string{"access.denied"}, ConfigCT: []byte("x"), CreatedBy: "root",
	}); err == nil {
		t.Fatal("duplicate name should conflict")
	}

	// Only enabled channels are returned to the dispatcher.
	dis, _ := s.NewID(ctx)
	if _, err := repo.CreateChannel(ctx, &NotificationChannel{
		ID: dis, Name: "disabled", Type: "webhook", Enabled: false,
		Events: []string{"access.denied"}, ConfigCT: []byte("x"), CreatedBy: "root",
	}); err != nil {
		t.Fatal(err)
	}
	enabled, err := repo.ListEnabledChannels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(enabled) != 1 || enabled[0].ID != id {
		t.Fatalf("ListEnabledChannels wrong: %+v", enabled)
	}

	// Cursor starts at the audit head (0 in a fresh reset DB) and FanOut advances
	// it; delivery inserts are idempotent on (channel_id, audit_seq).
	cur, err := repo.GetCursor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	d := NotificationDelivery{ChannelID: id, AuditSeq: 5, EventKind: "access.denied", Payload: []byte(`{"event":"access.denied"}`)}
	if err := repo.FanOut(ctx, []NotificationDelivery{d}, cur+5); err != nil {
		t.Fatal(err)
	}
	// Re-processing the same event (crash replay) inserts nothing new.
	if err := repo.FanOut(ctx, []NotificationDelivery{d}, cur+5); err != nil {
		t.Fatal(err)
	}
	after, _ := repo.GetCursor(ctx)
	if after != cur+5 {
		t.Fatalf("cursor not advanced: %d", after)
	}
	deliveries, err := repo.ListDeliveriesByChannel(ctx, id, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 1 {
		t.Fatalf("fan-out not idempotent: %d deliveries", len(deliveries))
	}

	// Delivery lifecycle: reschedule (retry), then deliver.
	dl := deliveries[0]
	if dl.Status != "pending" {
		t.Fatalf("new delivery not pending: %s", dl.Status)
	}
	future := time.Now().Add(time.Hour)
	if err := repo.RescheduleDelivery(ctx, dl.ID, future, "channel returned HTTP 500"); err != nil {
		t.Fatal(err)
	}
	// Not due now (scheduled an hour out).
	due, _ := repo.ClaimDueDeliveries(ctx, time.Now(), 10)
	if len(due) != 0 {
		t.Fatalf("rescheduled delivery should not be due: %d", len(due))
	}
	// Due once its time arrives.
	due, _ = repo.ClaimDueDeliveries(ctx, future.Add(time.Minute), 10)
	if len(due) != 1 || due[0].Attempts != 1 {
		t.Fatalf("reschedule bookkeeping wrong: %+v", due)
	}
	if err := repo.MarkDelivered(ctx, dl.ID); err != nil {
		t.Fatal(err)
	}
	final, _ := repo.ListDeliveriesByChannel(ctx, id, 10)
	if final[0].Status != "delivered" || final[0].DeliveredAt == nil {
		t.Fatalf("delivery not marked delivered: %+v", final[0])
	}

	// Deleting the channel cascades its deliveries.
	if err := repo.DeleteChannel(ctx, id); err != nil {
		t.Fatal(err)
	}
	gone, _ := repo.ListDeliveriesByChannel(ctx, id, 10)
	if len(gone) != 0 {
		t.Fatalf("deliveries not cascaded on channel delete: %d", len(gone))
	}
}
