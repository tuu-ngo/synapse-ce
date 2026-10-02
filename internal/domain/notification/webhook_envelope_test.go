package notification

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/KKloudTarus/synapse-ce/internal/testutil/eventschema"
)

// TestWebhookEnvelopeMatchesItsPublishedSchema keeps the Go envelope and
// docs/guide/schemas/notification-envelope.v1.schema.json in step: the envelope is a public
// contract (EPIC D4), so a field added in code without the schema, or the reverse, fails here.
func TestWebhookEnvelopeMatchesItsPublishedSchema(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "guide", "schemas", "notification-envelope.v1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	schema, err := eventschema.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := eventschema.Check(schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	full := Event{TenantID: "t", ID: "event-1", Type: EventScanCompleted, SourceKind: "scan_job", SourceID: "s1", SchemaVersion: 1,
		OccurredAt: time.Date(2026, 10, 1, 8, 0, 0, 0, time.FixedZone("ICT", 7*3600)), EngagementID: "eng-1", SubjectKind: "scan_job", SubjectID: "s1"}
	bare := Event{TenantID: "t", ID: "event-2", Type: EventTest, SourceKind: "notification_test", SourceID: "x", SchemaVersion: 1, OccurredAt: full.OccurredAt}
	for name, c := range map[string]struct {
		event Event
		vars  map[string]string
	}{
		"with subject and engagement": {full, map[string]string{"title": "Scan completed", "scan_kind": "sast"}},
		"without either":              {bare, nil},
	} {
		body, err := NewWebhookEnvelope(c.event, DataClassSummary, c.vars)
		if err != nil {
			t.Fatal(err)
		}
		var decoded any
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Fatal(err)
		}
		if err := eventschema.Match(schema, decoded); err != nil {
			t.Errorf("%s: %s does not match the schema: %v", name, body, err)
		}
	}
	if _, err := NewWebhookEnvelope(full, "secret", nil); err == nil {
		t.Fatal("an unknown class built an envelope")
	}
}

// TestWebhookEnvelopeIsUTC checks that occurred_at is written in UTC whatever zone the event holds.
func TestWebhookEnvelopeIsUTC(t *testing.T) {
	e := Event{ID: "e", Type: EventTest, OccurredAt: time.Date(2026, 10, 1, 15, 0, 0, 0, time.FixedZone("ICT", 7*3600))}
	body, err := NewWebhookEnvelope(e, DataClassSignal, nil)
	if err != nil {
		t.Fatal(err)
	}
	var decoded WebhookEnvelope
	if err := json.Unmarshal(body, &decoded); err != nil || decoded.OccurredAt.Location() != time.UTC || decoded.OccurredAt.Hour() != 8 {
		t.Fatalf("occurred_at = %v, %v", decoded.OccurredAt, err)
	}
}
