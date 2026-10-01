package notification

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/KKloudTarus/synapse-ce/internal/domain/msgtemplate"
	domain "github.com/KKloudTarus/synapse-ce/internal/domain/notification"
	"github.com/KKloudTarus/synapse-ce/internal/domain/tenancy"
	"github.com/KKloudTarus/synapse-ce/internal/usecase/ports"
)

// builtinSamples gives every catalog variable a distinctive value, so a golden file shows which
// variables each class lets through and a leak is easy to spot.
var builtinSamples = map[string]string{
	"occurred_at": "2026-09-27T08:00:00Z", "title": "Example title", "summary": "Example summary.",
	"severity": "high", "action_type": "escalation", "engagement_name": "Q3 audit", "scan_kind": "sast",
	"target": "https://git.example.test/org/repo.git", "failed_conditions": "2", "project_name": "Payments",
	"tier": "high", "deadline": "2026-09-28T08:00:00Z", "lead_time_hours": "24", "finding_title": "SQL injection in login",
	"last_seen_at": "2026-09-27T07:45:00Z", "agent_name": "node-42", "asset_name": "db-primary", "old_team": "Blue team",
	"new_team": "Red team", "old_assignee": "An Nguyen", "new_assignee": "Binh Tran", "actor": "actor-7", "reason": "routing rule",
}

var builtinClasses = []domain.DataClass{domain.DataClassSignal, domain.DataClassSummary, domain.DataClassDetail}

var builtinFamilies = []domain.TemplateFamily{domain.FamilyChat, domain.FamilyEmail, domain.FamilyPager, domain.FamilyWebhook}

// builtinEvents are the event types each built-in is rendered for: every rule-routed type, and
// notification.channel_paused for the generic "*" template.
func builtinEvents() []domain.EventType {
	var out []domain.EventType
	for _, spec := range domain.EventCatalog() {
		if !spec.OperatorOnly {
			out = append(out, spec.Type)
		}
	}
	return append(out, domain.EventChannelPaused)
}

// sampleVars is the event's snapshot at a class, as RenderMessage hands it to a template.
func sampleVars(t *testing.T, event domain.EventType, family domain.TemplateFamily, class domain.DataClass) map[string]string {
	t.Helper()
	spec, _ := domain.LookupEvent(event)
	full := domain.TemplateContext{Vars: map[string]string{"event_type": string(event), "event_label": spec.Label}}
	for name, value := range builtinSamples {
		full.Vars[name] = value
	}
	vars := full.Filter(spec, class).Vars
	if family == domain.FamilyWebhook {
		return vars
	}
	for _, v := range spec.Variables {
		if value, ok := vars[v.Name]; ok && v.Format == domain.VariableFormatTime {
			vars[v.Name] = presentTime(value, time.UTC)
		}
	}
	return vars
}

// renderBuiltin renders one built-in for one event at one class, field by field.
func renderBuiltin(t *testing.T, catalog ports.BuiltinTemplates, event domain.EventType, family domain.TemplateFamily, locale string, class domain.DataClass) map[string]string {
	t.Helper()
	b, ok := catalog.Builtin(event, family, tenancy.Locale(locale))
	if !ok {
		if b, ok = catalog.Builtin(domain.AnyEventType, family, tenancy.Locale(locale)); !ok {
			t.Fatalf("no %s %s built-in for %s", locale, family, event)
		}
	}
	vars := sampleVars(t, event, family, class)
	resolution := TemplateResolution{EventType: event, Family: family, Builtin: &b}
	if family == domain.FamilyWebhook {
		schemas, err := templateSchemas(event)
		if err != nil {
			t.Fatal(err)
		}
		body, err := domain.RenderCustomWebhookBody(b.Fields["body"], schemas[0].schema, msgtemplate.Data{Vars: vars})
		if err != nil {
			t.Fatalf("%s webhook for %s at %s: %v", locale, event, class, err)
		}
		return map[string]string{"body": string(body)}
	}
	fields, err := renderFields(resolution, b.Fields, vars)
	if err != nil {
		t.Fatalf("%s %s for %s at %s: %v", locale, family, event, class, err)
	}
	return fields
}

// TestBuiltinGolden is the #1366 golden test: every English built-in rendered for every event type
// at every data class, one file per family. Run with -update after a deliberate change.
func TestBuiltinGolden(t *testing.T) {
	catalog, err := NewBuiltinTemplates()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range builtinFamilies {
		var b strings.Builder
		for _, event := range builtinEvents() {
			for _, class := range builtinClasses {
				fields := renderBuiltin(t, catalog, event, family, "en", class)
				names := make([]string, 0, len(fields))
				for name := range fields {
					names = append(names, name)
				}
				sort.Strings(names)
				for _, name := range names {
					fmt.Fprintf(&b, "== %s %s %s\n%s\n", event, class, name, fields[name])
				}
			}
		}
		got := b.String()
		path := filepath.Join("testdata", "builtin", string(family)+".golden")
		if *updateGolden {
			if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read golden (run with -update to create it): %v", err)
		}
		if got != strings.ReplaceAll(string(want), "\r\n", "\n") {
			t.Errorf("%s built-ins changed; run with -update and review the diff", family)
		}
	}
}

// TestBuiltinsReadWellAtSignal checks the #1366 rule that every built-in works at signal: the
// headline is never empty, and no summary- or detail-class value reaches any field, in English or
// Vietnamese.
func TestBuiltinsReadWellAtSignal(t *testing.T) {
	catalog, err := NewBuiltinTemplates()
	if err != nil {
		t.Fatal(err)
	}
	headline := map[domain.TemplateFamily]string{domain.FamilyChat: "title", domain.FamilyEmail: "subject", domain.FamilyPager: "summary"}
	for _, locale := range []string{"en", "vi"} {
		for _, family := range builtinFamilies {
			for _, event := range builtinEvents() {
				fields := renderBuiltin(t, catalog, event, family, locale, domain.DataClassSignal)
				if field, ok := headline[family]; ok && strings.TrimSpace(fields[field]) == "" {
					t.Errorf("%s %s %s: empty %s at signal", locale, family, event, field)
				}
				spec, _ := domain.LookupEvent(event)
				for _, v := range spec.Variables {
					if v.Class == domain.DataClassSignal {
						continue
					}
					for name, text := range fields {
						if sample := builtinSamples[v.Name]; sample != "" && strings.Contains(text, sample) {
							t.Errorf("%s %s %s %s carries %s (%s) at signal", locale, family, event, name, v.Name, v.Class)
						}
					}
				}
			}
		}
	}
}
