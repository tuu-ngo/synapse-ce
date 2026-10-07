package notification

import (
	"errors"

	"github.com/KKloudTarus/synapse-ce/internal/domain/consolelink"
	domain "github.com/KKloudTarus/synapse-ce/internal/domain/notification"
	"github.com/KKloudTarus/synapse-ce/internal/domain/shared"
	"github.com/KKloudTarus/synapse-ce/internal/domain/tenancy"
	"github.com/KKloudTarus/synapse-ce/internal/usecase/ports"
)

// Deep links (#1348, #1367). A message links to the console page of its subject, built from the
// operator's SYNAPSE_PUBLIC_BASE_URL; a template can never supply a link of its own.

// SetLinkBuilder wires the console link builder. Without it messages carry no link.
func (s *Service) SetLinkBuilder(links consolelink.Builder) { s.links = &links }

// messageLinks returns the link to the event's subject, or none when the deployment has no public
// base URL or the subject has no console page.
func (s *Service) messageLinks(e domain.Event, locale tenancy.Locale) []ports.RenderedLink {
	if s.links == nil {
		return nil
	}
	link, err := subjectLink(*s.links, e)
	if err != nil {
		return nil
	}
	label := "Open in Synapse"
	if locale == "vi" {
		label = "Mở trong Synapse"
	}
	return []ports.RenderedLink{{Label: label, URL: link.Href()}}
}

// subjectLink picks the page: the finding, scan or incident the event is about, else its
// engagement.
func subjectLink(b consolelink.Builder, e domain.Event) (consolelink.Link, error) {
	subject := shared.ID(e.SubjectID)
	switch {
	case e.SubjectKind == "finding" && !e.EngagementID.IsZero() && !subject.IsZero():
		return b.Finding(e.EngagementID, subject)
	case e.SubjectKind == "scan_job" && !e.EngagementID.IsZero() && !subject.IsZero():
		return b.Scan(e.EngagementID, subject)
	case e.SubjectKind == "incident" && !subject.IsZero():
		return b.Incident(subject)
	case !e.EngagementID.IsZero():
		return b.Engagement(e.EngagementID)
	}
	return consolelink.Link{}, errNoConsolePage
}

var errNoConsolePage = errors.New("notification subject has no console page")
