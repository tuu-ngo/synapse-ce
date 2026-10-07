package notificationsender

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/KKloudTarus/synapse-ce/internal/domain/notification"
	"github.com/KKloudTarus/synapse-ce/internal/usecase/ports"
)

// webhookDriver posts the event envelope to a generic receiver, signed with HMAC-SHA256 over
// "<timestamp>.<body>" so the receiver can reject replays and forgeries.
//
// By default the body is the class-filtered envelope the send-time renderer built (#1367), sent with
// X-Synapse-Body: envelope. A channel an administrator switched to raw_event sends the raw event, as
// every webhook did before, with no X-Synapse-Body header.
//
// A channel that opted into a custom body (#1376) gets the rendered body the send-time renderer
// put in NotificationWork.CustomWebhookBody instead. The driver sends those exact bytes, signs the
// same bytes and sets X-Synapse-Body: custom, so the signature always covers the body as sent. It
// checks the bytes are one JSON value within the size cap and refuses the attempt otherwise; the
// renderer builds them with json.Marshal, so a refusal means a bug, not a tenant input.
type webhookDriver struct{ s *Sender }

func (webhookDriver) ChannelType() notification.ChannelType { return notification.ChannelWebhook }

func (d webhookDriver) Send(ctx context.Context, w ports.NotificationWork, config ports.NotificationChannelConfig) ports.NotificationSendResult {
	cfg, ok := config.(ports.WebhookChannelConfig)
	if !ok {
		return ports.NotificationSendResult{ErrorCode: "channel_config_invalid"}
	}
	custom := len(w.CustomWebhookBody) > 0
	envelope := !custom && len(w.WebhookEnvelope) > 0
	var body []byte
	switch {
	case custom:
		if len(w.CustomWebhookBody) > notification.MaxRenderedWebhookBodyBytes || !json.Valid(w.CustomWebhookBody) {
			return ports.NotificationSendResult{ErrorCode: "custom_body_invalid"}
		}
		body = w.CustomWebhookBody
	case envelope:
		if len(w.WebhookEnvelope) > notification.MaxRenderedWebhookBodyBytes || !json.Valid(w.WebhookEnvelope) {
			return ports.NotificationSendResult{ErrorCode: "envelope_invalid"}
		}
		body = w.WebhookEnvelope
	default:
		var err error
		if body, err = json.Marshal(w.Event); err != nil {
			return ports.NotificationSendResult{ErrorCode: "encode_failed"}
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL, bytes.NewReader(body))
	if err != nil {
		return ports.NotificationSendResult{ErrorCode: "request_invalid"}
	}
	timestamp := strconv.FormatInt(d.s.now().UTC().Unix(), 10)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "synapse-notifications/1")
	req.Header.Set("X-Synapse-Timestamp", timestamp)
	req.Header.Set("X-Synapse-Signature", webhookSignature(cfg.Secret, timestamp, body))
	req.Header.Set("X-Synapse-Event-ID", w.Event.ID.String())
	req.Header.Set("X-Synapse-Delivery-ID", w.Delivery.ID.String())
	switch {
	case custom:
		req.Header.Set(notification.CustomWebhookBodyHeader, "custom")
	case envelope:
		req.Header.Set(notification.CustomWebhookBodyHeader, notification.WebhookBodyEnvelope)
	}
	return d.s.do(req)
}

// webhookSignature is "sha256=" and the hex HMAC-SHA256 of "<timestamp>.<body>" under secret.
func webhookSignature(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
