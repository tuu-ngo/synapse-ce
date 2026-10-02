// Package builtin holds the message templates Synapse ships (EPIC #1327 T05, #1366): one per event
// type, channel family and locale, plus a generic "*" template per family and locale for event types
// without their own. They are written in the same template language as tenant templates, so a
// tenant can clone one and change it.
//
// Each file templates/<locale>/<family>.tmpl holds the templates of one family in one locale, as
// sections:
//
//	== <event type or *> <field>
//	<template source>
//
// A section runs to the next "==" line; its trailing line breaks are dropped. The package parses only:
// compiling against the event catalog happens where the templates are served, so this package stays
// free of the template engine.
package builtin

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/KKloudTarus/synapse-ce/internal/domain/notification"
	"github.com/KKloudTarus/synapse-ce/internal/domain/tenancy"
)

//go:embed templates
var files embed.FS

const (
	sectionMarker = "== "
	crlf          = "\r\n"
	lf            = "\n"
)

// Template is one shipped template.
type Template struct {
	notification.TemplateKey
	// Fields holds the source of each content field of the family.
	Fields map[string]string
}

// Ref is the reference a delivery records for t: builtin:<event>:<family>:<locale>@<build>.
func (t Template) Ref(build string) string {
	return fmt.Sprintf("builtin:%s:%s:%s@%s", t.EventType, t.Family, t.Locale, build)
}

// Set is every shipped template and the build identity of the set.
type Set struct {
	Templates []Template
	// Build identifies the content: the first 12 hex digits of a SHA-256 over every file. It
	// changes exactly when a template changes, so a recorded reference names the text that rendered.
	Build string
}

// Load parses the embedded templates.
func Load() (Set, error) {
	return load(files)
}

func load(fsys fs.FS) (Set, error) {
	var set Set
	digest := sha256.New()
	err := fs.WalkDir(fsys, "templates", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		raw, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		// Hash with LF line ends, so a checkout with CRLF gets the same build identity.
		text := strings.ReplaceAll(string(raw), crlf, lf)
		_, _ = digest.Write([]byte(name))
		_, _ = digest.Write([]byte(text))
		locale := tenancy.Locale(path.Base(path.Dir(name)))
		family := notification.TemplateFamily(strings.TrimSuffix(path.Base(name), ".tmpl"))
		templates, err := parseFile(text, family, locale)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		set.Templates = append(set.Templates, templates...)
		return nil
	})
	if err != nil {
		return Set{}, err
	}
	sort.Slice(set.Templates, func(i, j int) bool { return set.Templates[i].Ref("") < set.Templates[j].Ref("") })
	set.Build = hex.EncodeToString(digest.Sum(nil))[:12]
	return set, nil
}

// parseFile splits one family file into its templates, one per event type.
func parseFile(raw string, family notification.TemplateFamily, locale tenancy.Locale) ([]Template, error) {
	if !family.Valid() || !locale.Valid() {
		return nil, fmt.Errorf("unknown family %q or locale %q", family, locale)
	}
	byEvent := map[notification.EventType]map[string]string{}
	var order []notification.EventType
	var event notification.EventType
	var field string
	var body []string
	flush := func() error {
		if field == "" {
			return nil
		}
		fields := byEvent[event]
		if fields == nil {
			fields = map[string]string{}
			byEvent[event] = fields
			order = append(order, event)
		}
		if _, dup := fields[field]; dup {
			return fmt.Errorf("%s %s is defined twice", event, field)
		}
		fields[field] = strings.TrimRight(strings.Join(body, "\n"), "\n")
		return nil
	}
	for _, line := range strings.Split(raw, lf) {
		if !strings.HasPrefix(line, sectionMarker) {
			if field == "" && strings.TrimSpace(line) != "" {
				return nil, fmt.Errorf("text before the first section")
			}
			body = append(body, line)
			continue
		}
		if err := flush(); err != nil {
			return nil, err
		}
		parts := strings.Fields(strings.TrimPrefix(line, sectionMarker))
		if len(parts) != 2 {
			return nil, fmt.Errorf("section header %q must name an event type and a field", line)
		}
		event, field, body = notification.EventType(parts[0]), parts[1], nil
	}
	if err := flush(); err != nil {
		return nil, err
	}
	out := make([]Template, 0, len(order))
	for _, event := range order {
		t := Template{TemplateKey: notification.TemplateKey{EventType: event, Family: family, Locale: locale}, Fields: byEvent[event]}
		if err := t.Validate(); err != nil {
			return nil, err
		}
		if err := notification.ValidateTemplateFields(family, t.Fields); err != nil {
			return nil, fmt.Errorf("%s: %w", event, err)
		}
		out = append(out, t)
	}
	return out, nil
}
