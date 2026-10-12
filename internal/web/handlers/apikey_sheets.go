package handlers

import (
	"context"

	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/sheet"
)

// SheetResources lists a project's published sheets as the resources a project key can be pinned to.
func SheetResources(sheets *sheet.Service) APIKeyResources {
	return func(ctx context.Context, _ *project.Project) ([]APIKeyResource, error) {
		items, err := sheets.List(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]APIKeyResource, 0, len(items))
		for _, sh := range items {
			out = append(out, APIKeyResource{ID: sh.ID, Name: sh.Slug})
		}
		return out, nil
	}
}
