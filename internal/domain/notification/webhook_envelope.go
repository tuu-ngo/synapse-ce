package notification

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/KKloudTarus/synapse-ce/internal/domain/shared"
)

// Webhook envelope (EPIC #1327 D5, #1367). A generic webhook channel sends, by default, the event's
// template context filtered to the channel's effective data class, inside a versioned envelope. It
// is a public contract built in code, not a template, so it cannot fail to render and a tenant cannot
// change its shape. The raw event body is an administrator's opt-in (Channel.RawEvent) at detail.

// WebhookEnvelopeSchema names the envelope version; a breaking change bumps it.
const WebhookEnvelopeSchema = "synapse.notification.v1"

// WebhookBodyEnvelope and WebhookBodyRaw are the values of the X-Synapse-Body header that tell a
// receiver which body it got; a custom body (#1376) sends "custom".
const (
	WebhookBodyEnvelope = "envelope"
	WebhookBodyRaw      = "raw"
)

// WebhookEnvelope is the default webhook body.
type WebhookEnvelope struct {
	Schema     string    `json:"schema"`
	EventID    shared.ID `json:"event_id"`
	EventType  EventType `json:"event_type"`
	OccurredAt time.Time `json:"occurred_at"`
	// DataClass is the effective class the variables were filtered to.
	DataClass DataClass `json:"data_class"`
	Subject   *Subject  `json:"subject,omitempty"`
	// EngagementID is present only when the event has an engagement.
	EngagementID shared.ID `json:"engagement_id,omitempty"`
	// Variables are the catalog variables of the event type at or below DataClass, times in
	// RFC 3339 UTC.
	Variables map[string]string `json:"variables"`
}

// Subject names the entity an event is about.
type Subject struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// NewWebhookEnvelope builds the envelope of e at class from its filtered variables.
func NewWebhookEnvelope(e Event, class DataClass, vars map[string]string) ([]byte, error) {
	if !class.Valid() {
		return nil, invalidDataClass()
	}
	envelope := WebhookEnvelope{Schema: WebhookEnvelopeSchema, EventID: e.ID, EventType: e.Type, OccurredAt: e.OccurredAt.UTC(),
		DataClass: class, EngagementID: e.EngagementID, Variables: vars}
	if envelope.Variables == nil {
		envelope.Variables = map[string]string{}
	}
	if e.SubjectKind != "" && e.SubjectID != "" {
		envelope.Subject = &Subject{Kind: e.SubjectKind, ID: e.SubjectID}
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	if len(body) > MaxRenderedWebhookBodyBytes {
		return nil, fmt.Errorf("%w: webhook envelope is too large", shared.ErrValidation)
	}
	return body, nil
}
