package notification

import (
	"fmt"

	"github.com/KKloudTarus/synapse-ce/internal/domain/msgtemplate"
	domain "github.com/KKloudTarus/synapse-ce/internal/domain/notification"
	"github.com/KKloudTarus/synapse-ce/internal/domain/notification/builtin"
	"github.com/KKloudTarus/synapse-ce/internal/domain/tenancy"
	"github.com/KKloudTarus/synapse-ce/internal/usecase/ports"
)

// builtinCatalog serves the shipped templates (#1366) as ports.BuiltinTemplates.
type builtinCatalog struct {
	byKey map[domain.TemplateKey]ports.BuiltinTemplate
	all   []ports.BuiltinTemplate
}

var _ ports.BuiltinTemplates = (*builtinCatalog)(nil)

// NewBuiltinTemplates loads the shipped templates and checks that every one compiles against the
// variables of each event type it can render, as a tenant template is checked when it is saved. A
// template that does not compile is a build defect, so the error stops startup.
func NewBuiltinTemplates() (ports.BuiltinTemplates, error) {
	set, err := builtin.Load()
	if err != nil {
		return nil, fmt.Errorf("load built-in notification templates: %w", err)
	}
	catalog := &builtinCatalog{byKey: map[domain.TemplateKey]ports.BuiltinTemplate{}}
	for _, t := range set.Templates {
		if err := compileBuiltin(t); err != nil {
			return nil, fmt.Errorf("built-in template %s: %w", t.Ref(set.Build), err)
		}
		b := ports.BuiltinTemplate{Ref: t.Ref(set.Build), TemplateKey: t.TemplateKey, Fields: t.Fields}
		catalog.byKey[t.TemplateKey] = b
		catalog.all = append(catalog.all, b)
	}
	return catalog, nil
}

// compileBuiltin compiles every field for every event type the key covers.
func compileBuiltin(t builtin.Template) error {
	schemas, err := templateSchemas(t.EventType)
	if err != nil {
		return err
	}
	for _, field := range t.Family.Fields() {
		source := t.Fields[field]
		if source == "" {
			continue
		}
		if t.Family == domain.FamilyWebhook {
			if err := validateWebhookBody(t.EventType, source, schemas); err != nil {
				return err
			}
			continue
		}
		for _, schema := range schemas {
			if _, err := msgtemplate.Compile(field, source, schema.schema); err != nil {
				return fmt.Errorf("%s for %s: %w", field, schema.eventType, err)
			}
		}
	}
	return nil
}

func (c *builtinCatalog) Builtin(eventType domain.EventType, family domain.TemplateFamily, locale tenancy.Locale) (ports.BuiltinTemplate, bool) {
	b, ok := c.byKey[domain.TemplateKey{EventType: eventType, Family: family, Locale: locale}]
	return b, ok
}

func (c *builtinCatalog) List() []ports.BuiltinTemplate {
	return append([]ports.BuiltinTemplate(nil), c.all...)
}
