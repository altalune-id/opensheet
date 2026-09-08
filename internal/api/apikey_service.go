package api

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	apikeyv1 "altalune.id/opensheet/gen/go/apikey/v1"
	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/project"
)

// APIKeyService implements apikey.v1.APIKeyService.
type APIKeyService struct {
	keys     *apikey.Service
	projects *project.Service
}

// NewAPIKeyService binds the handler to its collaborators.
func NewAPIKeyService(keys *apikey.Service, projects *project.Service) *APIKeyService {
	return &APIKeyService{keys: keys, projects: projects}
}

// List returns the keys of the request's project.
func (s *APIKeyService) List(ctx context.Context, req *connect.Request[apikeyv1.ListRequest]) (*connect.Response[apikeyv1.ListResponse], error) {
	tctx, err := scopeToProject(ctx, s.projects, req.Msg.GetProjectId())
	if err != nil {
		return nil, err
	}
	items, err := s.keys.List(tctx)
	if err != nil {
		return nil, err
	}
	resp := &apikeyv1.ListResponse{ApiKeys: make([]*apikeyv1.APIKey, 0, len(items))}
	for _, k := range items {
		resp.ApiKeys = append(resp.ApiKeys, toAPIKeyProto(k))
	}
	return connect.NewResponse(resp), nil
}

// Create mints a key and returns its plaintext.
// SECURITY: plaintext_key is returned here and nowhere else; no other RPC can retrieve it.
func (s *APIKeyService) Create(ctx context.Context, req *connect.Request[apikeyv1.CreateRequest]) (*connect.Response[apikeyv1.CreateResponse], error) {
	tctx, err := scopeToProject(ctx, s.projects, req.Msg.GetProjectId())
	if err != nil {
		return nil, err
	}
	sheetIDs := make([]uuid.UUID, 0, len(req.Msg.GetSheetIds()))
	for _, raw := range req.Msg.GetSheetIds() {
		id, perr := parseUUID("sheet_ids", raw)
		if perr != nil {
			return nil, perr
		}
		sheetIDs = append(sheetIDs, id)
	}
	k, plaintext, err := s.keys.Create(tctx, apikey.CreateRequest{
		Name:      req.Msg.GetName(),
		Scopes:    req.Msg.GetScopes(),
		SheetIDs:  sheetIDs,
		ExpiresAt: optionalTime(req.Msg.GetExpiresAt()),
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&apikeyv1.CreateResponse{
		ApiKey:       toAPIKeyProto(k),
		PlaintextKey: plaintext,
	}), nil
}

// Revoke stops the referenced key authenticating.
func (s *APIKeyService) Revoke(ctx context.Context, req *connect.Request[apikeyv1.RevokeRequest]) (*connect.Response[apikeyv1.RevokeResponse], error) {
	tctx, err := scopeToProject(ctx, s.projects, req.Msg.GetProjectId())
	if err != nil {
		return nil, err
	}
	id, err := parseUUID("api_key_id", req.Msg.GetApiKeyId())
	if err != nil {
		return nil, err
	}
	k, err := s.keys.Revoke(tctx, id)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&apikeyv1.RevokeResponse{ApiKey: toAPIKeyProto(k)}), nil
}

// SECURITY: SecretHash is deliberately absent from the wire type; KeyPrefix is the public half only.
func toAPIKeyProto(k *apikey.APIKey) *apikeyv1.APIKey {
	out := &apikeyv1.APIKey{
		Id:         k.ID.String(),
		ProjectId:  k.ProjectID.String(),
		Name:       k.Name,
		KeyPrefix:  k.KeyPrefix,
		Scopes:     k.Scopes,
		SheetIds:   make([]string, 0, len(k.SheetIDs)),
		ExpiresAt:  optionalTimestamp(k.ExpiresAt),
		LastUsedAt: optionalTimestamp(k.LastUsedAt),
		RevokedAt:  optionalTimestamp(k.RevokedAt),
		CreatedAt:  timestamppb.New(k.CreatedAt),
	}
	for _, id := range k.SheetIDs {
		out.SheetIds = append(out.SheetIds, id.String())
	}
	return out
}

func optionalTimestamp(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}

func optionalTime(ts *timestamppb.Timestamp) *time.Time {
	if ts == nil {
		return nil
	}
	t := ts.AsTime()
	return &t
}
