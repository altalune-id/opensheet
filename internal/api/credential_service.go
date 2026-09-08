package api

import (
	"context"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	credentialv1 "altalune.id/opensheet/gen/go/credential/v1"
	"altalune.id/opensheet/internal/credential"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/project"
)

// CredentialService implements credential.v1.CredentialService.
type CredentialService struct {
	credentials     *credential.Service
	connectWorkflow *credential.ConnectWorkflow
	projects        *project.Service
}

// NewCredentialService binds the handler to its collaborators.
func NewCredentialService(
	credentials *credential.Service,
	connectWorkflow *credential.ConnectWorkflow,
	projects *project.Service,
) *CredentialService {
	return &CredentialService{credentials: credentials, connectWorkflow: connectWorkflow, projects: projects}
}

// List returns the credentials of the request's project.
func (s *CredentialService) List(ctx context.Context, req *connect.Request[credentialv1.ListRequest]) (*connect.Response[credentialv1.ListResponse], error) {
	tctx, err := scopeToProject(ctx, s.projects, req.Msg.GetProjectId())
	if err != nil {
		return nil, err
	}
	items, err := s.credentials.List(tctx)
	if err != nil {
		return nil, err
	}
	resp := &credentialv1.ListResponse{Credentials: make([]*credentialv1.Credential, 0, len(items))}
	for _, c := range items {
		resp.Credentials = append(resp.Credentials, toCredentialProto(c))
	}
	return connect.NewResponse(resp), nil
}

// UploadServiceAccount seals an uploaded Google service-account key into a new credential.
func (s *CredentialService) UploadServiceAccount(ctx context.Context, req *connect.Request[credentialv1.UploadServiceAccountRequest]) (*connect.Response[credentialv1.UploadServiceAccountResponse], error) {
	tctx, err := scopeToProject(ctx, s.projects, req.Msg.GetProjectId())
	if err != nil {
		return nil, err
	}
	c, err := s.credentials.UploadServiceAccount(tctx, req.Msg.GetName(), req.Msg.GetServiceAccountJson())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&credentialv1.UploadServiceAccountResponse{Credential: toCredentialProto(c)}), nil
}

// StartGoogleConnect returns the Google consent URL; the callback is a web route, so no redirect happens here.
func (s *CredentialService) StartGoogleConnect(ctx context.Context, req *connect.Request[credentialv1.StartGoogleConnectRequest]) (*connect.Response[credentialv1.StartGoogleConnectResponse], error) {
	tctx, err := scopeToProject(ctx, s.projects, req.Msg.GetProjectId())
	if err != nil {
		return nil, err
	}
	tc, err := tenant.From(tctx)
	if err != nil {
		return nil, err
	}
	// SECURITY: ConnectWorkflow.Start binds the grant to a person, and an API-key principal has no user - spec 6.2.
	if tc.UserID == uuid.Nil {
		return nil, forbiddenErr("google connect requires a user principal", "project_id", tc.ProjectID.String())
	}
	url, err := s.connectWorkflow.Start(tctx, tc.OrgID, tc.ProjectID, tc.UserID, req.Msg.GetReturnTo())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&credentialv1.StartGoogleConnectResponse{AuthorizationUrl: url}), nil
}

// Delete removes the referenced credential.
func (s *CredentialService) Delete(ctx context.Context, req *connect.Request[credentialv1.DeleteRequest]) (*connect.Response[credentialv1.DeleteResponse], error) {
	tctx, err := scopeToProject(ctx, s.projects, req.Msg.GetProjectId())
	if err != nil {
		return nil, err
	}
	id, err := parseUUID("credential_id", req.Msg.GetCredentialId())
	if err != nil {
		return nil, err
	}
	if err := s.credentials.Delete(tctx, id); err != nil {
		return nil, err
	}
	return connect.NewResponse(&credentialv1.DeleteResponse{}), nil
}

// SECURITY: Credential.Sealed is deliberately absent from the wire type; adding it would publish ciphertext to every client.
func toCredentialProto(c *credential.Credential) *credentialv1.Credential {
	return &credentialv1.Credential{
		Id:                 c.ID.String(),
		ProjectId:          c.ProjectID.String(),
		Name:               c.Name,
		Kind:               string(c.Kind),
		Status:             string(c.Status),
		GoogleAccountEmail: c.GoogleAccountEmail,
		CreatedAt:          timestamppb.New(c.CreatedAt),
		UpdatedAt:          timestamppb.New(c.UpdatedAt),
	}
}
