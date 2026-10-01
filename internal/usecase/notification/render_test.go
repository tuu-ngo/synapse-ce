package notification

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/KKloudTarus/synapse-ce/internal/domain/consolelink"
	domain "github.com/KKloudTarus/synapse-ce/internal/domain/notification"
	"github.com/KKloudTarus/synapse-ce/internal/domain/shared"
	"github.com/KKloudTarus/synapse-ce/internal/infrastructure/messageformat"
	"github.com/KKloudTarus/synapse-ce/internal/infrastructure/persistence/memory"
	"github.com/KKloudTarus/synapse-ce/internal/infrastructure/vault"
	"github.com/KKloudTarus/synapse-ce/internal/platform/idgen"
	"github.com/KKloudTarus/synapse-ce/internal/usecase/ports"
)

type stepClock struct{ at time.Time }

func (c *stepClock) Now() time.Time { return c.at }

// scriptedSender answers each send with the next result and keeps what it was asked to send.
type scriptedSender struct {
	results []ports.NotificationSendResult
	sent    []ports.NotificationWork
}

func (s *scriptedSender) Send(_ context.Context, w ports.NotificationWork, _ ports.NotificationChannelConfig) ports.NotificationSendResult {
	s.sent = append(s.sent, w)
	result := ports.NotificationSendResult{StatusCode: 204}
	if len(s.results) > 0 {
		result, s.results = s.results[0], s.results[1:]
	}
	return result
}

type discardRenderAudit struct{}

func (discardRenderAudit) Record(context.Context, ports.AuditEntry) error { return nil }

type renderHarness struct {
	ctx       context.Context
	clock     *stepClock
	jobs      *memory.JobQueue
	repo      *memory.NotificationRepository
	templates *memory.NotificationTemplateStore
	sender    *scriptedSender
	svc       *Service
	channel   domain.Channel
}

func newRenderHarness(t *testing.T) *renderHarness {
	t.Helper()
	h := &renderHarness{ctx: shared.WithTenant(context.Background(), "tenant-r"), clock: &stepClock{at: time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)}, sender: &scriptedSender{}}
	h.jobs = memory.NewJobQueue(idgen.RandomID{}, h.clock.Now)
	h.repo = memory.NewNotificationRepository(h.jobs, h.clock.Now)
	h.repo.SetEventProjector(NewEventBuilders())
	cipher, err := vault.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	if h.svc, err = NewService(h.repo, cipher, h.sender, discardRenderAudit{}, h.clock, idgen.RandomID{}); err != nil {
		t.Fatal(err)
	}
	h.templates = memory.NewNotificationTemplateStore()
	h.svc.SetTemplateStore(h.templates)
	h.svc.SetFormatters(messageformat.Formatters())
	if h.channel, err = h.svc.CreateChannel(h.ctx, "admin", ChannelInput{Name: "Room", Type: domain.ChannelSlack, Enabled: true, URL: "https://hooks.slack.com/services/T/B/x", DataClass: classOf(domain.DataClassSummary), AllowClassRaise: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.CreateRule(h.ctx, "admin", RuleInput{Name: "Scans", Enabled: true, EventType: domain.EventScanCompleted, ChannelIDs: []shared.ID{h.channel.ID}}); err != nil {
		t.Fatal(err)
	}
	return h
}

func classOf(c domain.DataClass) *domain.DataClass { return &c }

// template creates and activates a chat template for scan.completed.
func (h *renderHarness) template(t *testing.T, title string) TemplateDetail {
	t.Helper()
	created, err := h.svc.CreateTemplate(h.ctx, "admin", TemplateInput{Name: "Scans", EventType: domain.EventScanCompleted, Family: domain.FamilyChat, Locale: "en",
		Fields: map[string]string{"title": title, "body": "Scan of {{.scan_kind}} finished at {{.occurred_at}}"}})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	active, err := h.svc.ActivateTemplate(h.ctx, "admin", created.ID, TemplateChangeInput{Revision: created.Revision})
	if err != nil {
		t.Fatalf("activate template: %v", err)
	}
	return active
}

// publish records one scan.completed event and returns its delivery job.
func (h *renderHarness) publish(t *testing.T, scan string) ports.QueuedJob {
	t.Helper()
	e := domain.Event{TenantID: "tenant-r", ID: shared.ID("event-" + scan), Type: domain.EventScanCompleted, SourceKind: "scan_job", SourceID: scan, SchemaVersion: 1,
		OccurredAt: h.clock.at, Data: json.RawMessage(`{"title":"Scan completed","summary":"A scan completed successfully.","scan_id":"` + scan + `","scan_kind":"sast"}`)}
	if _, err := h.repo.Publish(h.ctx, e); err != nil {
		t.Fatal(err)
	}
	return h.claim(t)
}

func (h *renderHarness) claim(t *testing.T) ports.QueuedJob {
	t.Helper()
	job, err := h.jobs.Claim(h.ctx, time.Minute, JobKind)
	if err != nil || job == nil {
		t.Fatalf("claim = %v, %v", job, err)
	}
	return *job
}

func (h *renderHarness) delivery(t *testing.T, job ports.QueuedJob) (domain.Delivery, []domain.Attempt) {
	t.Helper()
	var payload struct {
		DeliveryID shared.ID `json:"delivery_id"`
	}
	_ = json.Unmarshal(job.Payload, &payload)
	d, err := h.repo.GetDelivery(h.ctx, "tenant-r", payload.DeliveryID)
	if err != nil {
		t.Fatal(err)
	}
	attempts, err := h.repo.ListAttempts(h.ctx, "tenant-r", payload.DeliveryID)
	if err != nil {
		t.Fatal(err)
	}
	return d, attempts
}

func sentText(w ports.NotificationWork) string {
	if w.Formatted == nil {
		return ""
	}
	return string(w.Formatted.Body)
}

// TestRetryReusesThePinnedTemplate is the #1365 acceptance test: a retry renders with the version
// its first attempt pinned even after another version is activated, while a new delivery renders
// with the new one.
func TestRetryReusesThePinnedTemplate(t *testing.T) {
	h := newRenderHarness(t)
	first := h.template(t, "v1 {{.title}}")
	h.sender.results = []ports.NotificationSendResult{{StatusCode: 503, ErrorCode: "http_503", Retryable: true}}
	job := h.publish(t, "scan-1")
	if err := h.svc.HandleJob(h.ctx, job); err == nil {
		t.Fatal("a 503 must be retried")
	}
	pin := "tenant:" + first.ID.String() + "@1"
	d, attempts := h.delivery(t, job)
	if d.TemplateRef != pin || len(attempts) != 1 || attempts[0].TemplateRef != pin {
		t.Fatalf("pin = %q, attempts = %+v, want %s", d.TemplateRef, attempts, pin)
	}
	if !strings.Contains(sentText(h.sender.sent[0]), "v1 Scan completed") || !strings.Contains(sentText(h.sender.sent[0]), "2026-10-01 08:00 UTC") {
		t.Fatalf("first attempt sent %s", sentText(h.sender.sent[0]))
	}

	updated, err := h.svc.UpdateTemplate(h.ctx, "admin", first.ID, TemplateUpdateInput{Fields: map[string]string{"title": "v2 {{.title}}", "body": "new"}, Revision: first.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.ActivateTemplate(h.ctx, "admin", first.ID, TemplateChangeInput{Version: 2, Revision: updated.Revision}); err != nil {
		t.Fatal(err)
	}

	if err := h.jobs.Retry(h.ctx, job.ID, job.Fence, time.Second); err != nil {
		t.Fatal(err)
	}
	h.clock.at = h.clock.at.Add(2 * time.Second)
	retry := h.claim(t)
	if err := h.svc.HandleJob(h.ctx, retry); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if got := sentText(h.sender.sent[1]); !strings.Contains(got, "v1 Scan completed") {
		t.Fatalf("the retry rendered %s, want the pinned version 1", got)
	}
	if _, attempts := h.delivery(t, retry); len(attempts) != 2 || attempts[1].TemplateRef != pin {
		t.Fatalf("retry attempts = %+v", attempts)
	}

	h.clock.at = h.clock.at.Add(2 * time.Second) // past the per-channel rate limit
	fresh := h.publish(t, "scan-2")
	if err := h.svc.HandleJob(h.ctx, fresh); err != nil {
		t.Fatal(err)
	}
	if got := sentText(h.sender.sent[2]); !strings.Contains(got, "v2 Scan completed") {
		t.Fatalf("a new delivery rendered %s, want version 2", got)
	}
}

// TestFailingTemplateFallsBackAndDelivers is the second #1365 acceptance test: a template that no
// longer renders falls back to the driver's built-in content, records the fallback, and the
// delivery still succeeds.
func TestFailingTemplateFallsBackAndDelivers(t *testing.T) {
	h := newRenderHarness(t)
	// Stored directly, as a template saved before a catalog change would be: it names a variable
	// the event no longer declares, so it no longer compiles.
	now := h.clock.at
	broken, _, err := h.templates.CreateNotificationTemplate(h.ctx, domain.Template{TenantID: "tenant-r", ID: "broken", Name: "Broken",
		TemplateKey: domain.TemplateKey{EventType: domain.EventScanCompleted, Family: domain.FamilyChat, Locale: "en"},
		Status:      domain.TemplateDraft, Revision: 1, CreatedAt: now, CreatedBy: "admin", UpdatedAt: now, UpdatedBy: "admin"},
		map[string]string{"title": "{{.retired_variable}}", "body": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.templates.ActivateNotificationTemplate(h.ctx, ports.NotificationTemplateChange{TenantID: "tenant-r", ID: broken.ID, ExpectedRevision: broken.Revision, Actor: "admin", At: now}); err != nil {
		t.Fatal(err)
	}
	job := h.publish(t, "scan-1")
	if err := h.svc.HandleJob(h.ctx, job); err != nil {
		t.Fatalf("handle job: %v", err)
	}
	if h.sender.sent[0].Formatted != nil {
		t.Fatal("a template that failed to render still produced a payload")
	}
	d, attempts := h.delivery(t, job)
	if d.State != domain.DeliverySucceeded || d.TemplateRef != refFallback || attempts[0].TemplateRef != refFallback {
		t.Fatalf("delivery = %+v, attempts = %+v, want delivered with the fallback recorded", d, attempts)
	}
}

// TestRenderMessageAppliesTheDataClass checks that a variable above the channel's class renders
// empty, and that an engagement set to none suppresses the message.
func TestRenderMessageAppliesTheDataClass(t *testing.T) {
	h := newRenderHarness(t)
	h.template(t, "{{.title}} [{{.scan_kind}}]")
	e := domain.Event{TenantID: "tenant-r", ID: "event-x", Type: domain.EventScanCompleted, SourceKind: "scan_job", SourceID: "x", SchemaVersion: 1,
		OccurredAt: h.clock.at, Data: json.RawMessage(`{"title":"Scan completed","scan_kind":"sast"}`)}
	projected, err := NewEventBuilders().Project(h.ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	signal := h.channel
	signal.DataClass = domain.DataClassSignal
	out, err := h.svc.RenderMessage(h.ctx, RenderInput{Channel: signal, Event: projected})
	if err != nil {
		t.Fatal(err)
	}
	if got := out.Message.Fields["title"]; got != " [sast]" {
		t.Fatalf("signal-class title = %q, want the summary-class title left out", got)
	}
	suppressed, err := h.svc.RenderMessage(h.ctx, RenderInput{Channel: h.channel, Event: projected, Engagement: domain.EngagementNotificationsNone})
	if err != nil || !suppressed.Suppressed {
		t.Fatalf("engagement none = %+v, %v", suppressed, err)
	}
}

func (h *renderHarness) webhook(t *testing.T, in ChannelInput) domain.Channel {
	t.Helper()
	in.Name, in.Type, in.Enabled, in.URL, in.Secret = "Hook", domain.ChannelWebhook, true, "https://hooks.example.test/in", "0123456789abcdef"
	c, err := h.svc.CreateChannel(h.ctx, "admin", in)
	if err != nil {
		t.Fatalf("create webhook: %v", err)
	}
	return c
}

func incidentEvent(t *testing.T, at time.Time) domain.Event {
	t.Helper()
	seed, _ := domain.TemplateContext{Vars: map[string]string{"asset_name": "db-primary", "engagement_name": "Q3 audit"}}.Encode()
	e := domain.Event{TenantID: "tenant-r", ID: "event-i", Type: domain.EventIncidentCreated, SourceKind: "incident", SourceID: "inc-1", SchemaVersion: 1,
		EngagementID: "eng-1", Severity: "high", OccurredAt: at, Data: json.RawMessage(`{"title":"Suspicious login","incident_id":"inc-1","asset_id":"a1"}`), Context: seed}
	projected, err := NewEventBuilders().Project(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	return projected
}

// TestWebhookDefaultsToTheEnvelope checks the #1367 default body: the class-filtered variables in
// a versioned envelope, times in RFC 3339 UTC, and nothing above the channel's class.
func TestWebhookDefaultsToTheEnvelope(t *testing.T) {
	h := newRenderHarness(t)
	hook := h.webhook(t, ChannelInput{})
	out, err := h.svc.RenderMessage(h.ctx, RenderInput{Channel: hook, Event: incidentEvent(t, h.clock.at)})
	if err != nil {
		t.Fatal(err)
	}
	var envelope domain.WebhookEnvelope
	if err := json.Unmarshal(out.Envelope, &envelope); err != nil {
		t.Fatalf("envelope %s: %v", out.Envelope, err)
	}
	if out.Message.TemplateRef != refEnvelope || envelope.Schema != domain.WebhookEnvelopeSchema || envelope.DataClass != domain.DataClassSummary ||
		envelope.Subject == nil || envelope.Subject.Kind != "incident" || envelope.EngagementID != "eng-1" {
		t.Fatalf("envelope = %+v, ref %q", envelope, out.Message.TemplateRef)
	}
	if envelope.Variables["title"] != "Suspicious login" || envelope.Variables["engagement_name"] != "Q3 audit" || envelope.Variables["occurred_at"] != "2026-10-01T08:00:00Z" {
		t.Fatalf("variables = %v", envelope.Variables)
	}
	if _, leaked := envelope.Variables["asset_name"]; leaked {
		t.Fatal("a detail-class variable reached a summary-class webhook")
	}
}

// TestRawEventIsAnAdministratorOptIn checks that the raw body needs the detail class and the
// administer capability, and that a raw channel renders no envelope.
func TestRawEventIsAnAdministratorOptIn(t *testing.T) {
	h := newRenderHarness(t)
	yes := true
	if _, err := h.svc.CreateChannel(h.ctx, "admin", ChannelInput{Name: "Raw", Type: domain.ChannelWebhook, Enabled: true, URL: "https://hooks.example.test/raw",
		Secret: "0123456789abcdef", RawEvent: &yes}); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("raw at summary = %v, want ErrValidation", err)
	}
	raw := h.webhook(t, ChannelInput{RawEvent: &yes, DataClass: classOf(domain.DataClassDetail)})
	out, err := h.svc.RenderMessage(h.ctx, RenderInput{Channel: raw, Event: incidentEvent(t, h.clock.at)})
	if err != nil || out.Envelope != nil || out.Message.TemplateRef != refRawEvent {
		t.Fatalf("raw render = %+v, %v", out, err)
	}

	hook := h.webhook(t, ChannelInput{DataClass: classOf(domain.DataClassDetail)})
	enable := ChannelInput{Name: hook.Name, Enabled: true, Revision: hook.Revision, RawEvent: &yes}
	if _, err := h.svc.UpdateChannel(h.ctx, "integrator", hook.ID, enable); !errors.Is(err, shared.ErrForbidden) {
		t.Fatalf("enabling raw without administer = %v, want ErrForbidden", err)
	}
	enable.AllowClassRaise = true
	if updated, err := h.svc.UpdateChannel(h.ctx, "admin", hook.ID, enable); err != nil || !updated.RawEvent {
		t.Fatalf("enabling raw with administer = %+v, %v", updated, err)
	}
}

// TestChatMessagesLinkToTheirSubject checks the deep link and the Slack unfurl settings: a message
// rendered from a built-in links to its subject's console page under the public base URL.
func TestChatMessagesLinkToTheirSubject(t *testing.T) {
	h := newRenderHarness(t)
	builtins, err := NewBuiltinTemplates()
	if err != nil {
		t.Fatal(err)
	}
	h.svc.SetBuiltinTemplates(builtins)
	links, err := consolelink.NewBuilder("https://synapse.example.test")
	if err != nil {
		t.Fatal(err)
	}
	h.svc.SetLinkBuilder(links)
	out, err := h.svc.RenderMessage(h.ctx, RenderInput{Channel: h.channel, Event: incidentEvent(t, h.clock.at)})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Message.Links) != 1 || !strings.HasPrefix(out.Message.Links[0].URL, "https://synapse.example.test/") || !strings.Contains(out.Message.Links[0].URL, "inc-1") {
		t.Fatalf("links = %+v", out.Message.Links)
	}
	if out.Formatted == nil || !strings.Contains(string(out.Formatted.Body), `"unfurl_links":false`) || !strings.Contains(string(out.Formatted.Body), out.Message.Links[0].URL) {
		t.Fatalf("slack payload = %s", out.Formatted.Body)
	}
	if !strings.HasPrefix(out.Message.TemplateRef, "builtin:incident.created:chat:en@") {
		t.Fatalf("ref = %q, want the built-in", out.Message.TemplateRef)
	}
}
