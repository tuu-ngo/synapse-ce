package builtin

import (
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/KKloudTarus/synapse-ce/internal/domain/notification"
)

var families = []notification.TemplateFamily{notification.FamilyChat, notification.FamilyEmail, notification.FamilyPager, notification.FamilyWebhook}

// TestEveryEventHasATemplateInEveryFamily checks the set the resolver relies on: a generic "*"
// template and one per rule-routed event type, for every family, in English.
func TestEveryEventHasATemplateInEveryFamily(t *testing.T) {
	set, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	have := map[notification.TemplateKey]bool{}
	for _, tpl := range set.Templates {
		have[tpl.TemplateKey] = true
	}
	for _, family := range families {
		want := []notification.EventType{notification.AnyEventType}
		for _, spec := range notification.EventCatalog() {
			if !spec.OperatorOnly {
				want = append(want, spec.Type)
			}
		}
		for _, event := range want {
			if !have[notification.TemplateKey{EventType: event, Family: family, Locale: "en"}] {
				t.Errorf("no English %s template for %s", family, event)
			}
		}
	}
}

// TestVietnameseIsComplete is the #1366 completeness test: every English template has a
// Vietnamese counterpart with the same fields, and nothing exists only in Vietnamese.
func TestVietnameseIsComplete(t *testing.T) {
	set, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[notification.TemplateKey]Template{}
	for _, tpl := range set.Templates {
		byKey[tpl.TemplateKey] = tpl
	}
	for key, en := range byKey {
		other := key
		switch key.Locale {
		case "en":
			other.Locale = "vi"
		case "vi":
			other.Locale = "en"
		default:
			t.Fatalf("unexpected locale %q", key.Locale)
		}
		counterpart, ok := byKey[other]
		if !ok {
			t.Errorf("%s has no %s counterpart", key, other.Locale)
			continue
		}
		for field := range en.Fields {
			if strings.TrimSpace(counterpart.Fields[field]) == "" {
				t.Errorf("%s %s has no %s counterpart", key, field, other.Locale)
			}
		}
	}
}

func TestBuildIdentifiesTheContent(t *testing.T) {
	set, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{12}$`).MatchString(set.Build) {
		t.Fatalf("build = %q", set.Build)
	}
	tpl := set.Templates[0]
	if want := "builtin:" + string(tpl.EventType) + ":" + string(tpl.Family) + ":" + string(tpl.Locale) + "@" + set.Build; tpl.Ref(set.Build) != want {
		t.Fatalf("ref = %q, want %q", tpl.Ref(set.Build), want)
	}

	lf := fstest.MapFS{"templates/en/pager.tmpl": {Data: []byte("== * summary\nA\n")}}
	crlf := fstest.MapFS{"templates/en/pager.tmpl": {Data: []byte("== * summary\r\nA\r\n")}}
	changed := fstest.MapFS{"templates/en/pager.tmpl": {Data: []byte("== * summary\nB\n")}}
	a, errA := load(lf)
	b, errB := load(crlf)
	c, errC := load(changed)
	if errA != nil || errB != nil || errC != nil {
		t.Fatal(errA, errB, errC)
	}
	if a.Build != b.Build || a.Build == c.Build {
		t.Fatalf("builds lf=%s crlf=%s changed=%s: line ends must not matter, content must", a.Build, b.Build, c.Build)
	}
	if got := b.Templates[0].Fields["summary"]; got != "A" {
		t.Fatalf("CRLF field = %q", got)
	}
}

func TestLoadRefusesMalformedFiles(t *testing.T) {
	for name, content := range map[string]string{
		"text before a section":   "stray\n== * summary\nA\n",
		"duplicate field":         "== * summary\nA\n== * summary\nB\n",
		"header without field":    "== *\nA\n",
		"unknown event":           "== nope.event summary\nA\n",
		"field of another family": "== * title\nA\n",
	} {
		_, err := load(fstest.MapFS{"templates/en/pager.tmpl": {Data: []byte(content)}})
		if err == nil {
			t.Errorf("%s: loaded", name)
		}
	}
	if _, err := load(fstest.MapFS{"templates/xx/pager.tmpl": {Data: []byte("== * summary\nA\n")}}); err == nil {
		t.Error("an unknown locale loaded")
	}
}
