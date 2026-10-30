package cli

import (
	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"altalune.id/opensheet/internal/platform/session"
)

// Principal is the local identity an in-process command acts as.
type Principal struct {
	UserID    uuid.UUID
	Email     string
	Name      string
	OrgID     uuid.UUID
	ProjectID uuid.UUID
	Source    string
}

// SECURITY: only a local profile names a user of this database; a remote profile's ids belong to another server.
func resolveLocal(cmd *cobra.Command) (Principal, error) {
	store, err := sessionStore(cmd)
	if err != nil {
		return Principal{}, err
	}
	name := profileName(cmd)
	p, err := store.load(name)
	if err != nil {
		return Principal{}, err
	}
	if p == nil {
		return Principal{}, &NotSignedInError{Profile: name, Local: true}
	}
	if p.URL != "" {
		return Principal{}, &NotSignedInError{Profile: name, RemoteURL: p.URL}
	}
	v := p.Principal
	return Principal{
		UserID:    parseID(v.UserID),
		Email:     v.Email,
		Name:      v.Name,
		OrgID:     parseID(v.ActiveOrgID),
		ProjectID: parseID(v.ActiveProjectID),
		Source:    v.Source,
	}, nil
}

func parseID(s string) uuid.UUID {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil
	}
	return id
}

func idString(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}

func viewOfSession(p session.Principal) principalView {
	return principalView{
		Source:          string(p.Source),
		UserID:          idString(p.UserID),
		Email:           p.Email,
		Name:            p.Name,
		ActiveOrgID:     idString(p.ActiveOrgID),
		ActiveProjectID: idString(p.ActiveProjectID),
	}
}
