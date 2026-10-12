package handlers

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/web/templates"
)

// APIKeyResource is one resource inside a project that a project key can be pinned to.
type APIKeyResource struct {
	ID   uuid.UUID
	Name string
}

// APIKeyResources lists the resources of p that a project key can be pinned to.
type APIKeyResources func(ctx context.Context, p *project.Project) ([]APIKeyResource, error)

// ResourceNotInProjectError reports a submitted resource id that is not one of the project's resources.
type ResourceNotInProjectError struct{ ID string }

func (e *ResourceNotInProjectError) Error() string {
	return "web apikey: resource " + e.ID + " is not in this project"
}

// IsResourceNotInProjectError reports whether err is a *ResourceNotInProjectError.
func IsResourceNotInProjectError(err error) bool {
	_, ok := errors.AsType[*ResourceNotInProjectError](err)
	return ok
}

// WithResources lets project keys be pinned to the resources list returns; without it keys stay project-wide.
func (h *APIKeyHandler) WithResources(list APIKeyResources) *APIKeyHandler {
	h.Resources = list
	return h
}

func (h *APIKeyHandler) resources(sc ProjectScope) ([]APIKeyResource, error) {
	if h.Resources == nil {
		return nil, nil
	}
	return h.Resources(sc.req.Context(), sc.project)
}

func (h *APIKeyHandler) pinnedResources(sc ProjectScope, raw []string) ([]uuid.UUID, error) {
	if h.Resources == nil {
		return nil, nil
	}
	options, err := h.resources(sc)
	if err != nil {
		return nil, err
	}
	return pickResources(raw, options)
}

// SECURITY: every submitted id must be one of the project's own resources — the form is never trusted.
func pickResources(raw []string, options []APIKeyResource) ([]uuid.UUID, error) {
	var out []uuid.UUID
	for _, s := range raw {
		id, err := uuid.Parse(strings.TrimSpace(s))
		if err != nil || !slices.ContainsFunc(options, func(o APIKeyResource) bool { return o.ID == id }) {
			return nil, &ResourceNotInProjectError{ID: s}
		}
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out, nil
}

func resourceView(options []APIKeyResource) []templates.APIKeyResourceOption {
	out := make([]templates.APIKeyResourceOption, 0, len(options))
	for _, o := range options {
		out = append(out, templates.APIKeyResourceOption{ID: o.ID.String(), Name: o.Name})
	}
	return out
}

func resourceNames(ids []uuid.UUID, options []APIKeyResource) []string {
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		i := slices.IndexFunc(options, func(o APIKeyResource) bool { return o.ID == id })
		if i < 0 {
			names = append(names, id.String())
			continue
		}
		names = append(names, options[i].Name)
	}
	return names
}
