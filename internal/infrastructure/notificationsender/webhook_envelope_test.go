package notificationsender

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/KKloudTarus/synapse-ce/internal/domain/notification"
	"github.com/KKloudTarus/synapse-ce/internal/usecase/ports"
)

// sendWebhook posts one delivery to a fake receiver and returns what it got.
func sendWebhook(t *testing.T, work ports.NotificationWork) (capturedRequest, ports.NotificationSendResult) {
	t.Helper()
	var captured capturedRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.body = readAll(t, r)
		captured.header = r.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	s := New(SMTPConfig{}, time.Second)
	s.http = server.Client()
	s.now = func() time.Time { return time.Unix(1700000100, 0) }
	result := s.Send(context.Background(), work, ports.WebhookChannelConfig{URL: server.URL, Secret: customBodySecret})
	return captured, result
}

// TestWebhookSendsAndSignsTheEnvelope is the #1367 contract test: a receiver gets the envelope
// byte for byte, marked as such, with a signature it can verify over exactly those bytes, and the
// same delivery headers as before.
func TestWebhookSendsAndSignsTheEnvelope(t *testing.T) {
	work := goldenWork(notification.ChannelWebhook)
	envelope, err := notification.NewWebhookEnvelope(work.Event, notification.DataClassSummary, map[string]string{"title": "Scan completed", "scan_kind": "sast"})
	if err != nil {
		t.Fatal(err)
	}
	work.WebhookEnvelope = envelope
	got, result := sendWebhook(t, work)
	if result.ErrorCode != "" {
		t.Fatalf("send = %+v", result)
	}
	if !bytes.Equal(got.body, envelope) {
		t.Fatalf("sent %s, built %s", got.body, envelope)
	}
	if got.header.Get("X-Synapse-Body") != notification.WebhookBodyEnvelope || got.header.Get("X-Synapse-Event-ID") != work.Event.ID.String() ||
		got.header.Get("X-Synapse-Delivery-ID") != work.Delivery.ID.String() {
		t.Fatalf("headers = %v", got.header)
	}
	verifySignature(t, got.header, got.body)
	var decoded notification.WebhookEnvelope
	if err := json.Unmarshal(got.body, &decoded); err != nil || decoded.Schema != notification.WebhookEnvelopeSchema || decoded.DataClass != notification.DataClassSummary ||
		decoded.Variables["scan_kind"] != "sast" {
		t.Fatalf("receiver sees %+v, %v", decoded, err)
	}
}

// A channel switched to the raw event (every channel that existed before #1367) still sends the raw
// event with no X-Synapse-Body header, so its receiver sees no change.
func TestWebhookRawEventIsUnchanged(t *testing.T) {
	work := goldenWork(notification.ChannelWebhook)
	got, result := sendWebhook(t, work)
	if result.ErrorCode != "" || got.header.Get("X-Synapse-Body") != "" {
		t.Fatalf("raw send = %+v, header %q", result, got.header.Get("X-Synapse-Body"))
	}
	raw, _ := json.Marshal(work.Event)
	if !bytes.Equal(got.body, raw) {
		t.Fatalf("raw body = %s, want the event %s", got.body, raw)
	}
	verifySignature(t, got.header, got.body)
}

// A custom body wins over the envelope; an envelope that is not one JSON value is never sent.
func TestWebhookBodyPrecedenceAndEnvelopeChecks(t *testing.T) {
	work := goldenWork(notification.ChannelWebhook)
	work.CustomWebhookBody = []byte(`{"custom":true}`)
	work.WebhookEnvelope = []byte(`{"schema":"synapse.notification.v1"}`)
	if got, _ := sendWebhook(t, work); got.header.Get("X-Synapse-Body") != "custom" || string(got.body) != `{"custom":true}` {
		t.Fatalf("custom body lost to the envelope: %s %v", got.body, got.header)
	}
	work.CustomWebhookBody, work.WebhookEnvelope = nil, []byte(`{"broken":`)
	if _, result := sendWebhook(t, work); result.ErrorCode != "envelope_invalid" || result.Retryable {
		t.Fatalf("invalid envelope = %+v", result)
	}
}
