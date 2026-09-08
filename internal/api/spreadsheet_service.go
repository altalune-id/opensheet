package api

import (
	"context"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	spreadsheetv1 "altalune.id/opensheet/gen/go/spreadsheet/v1"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/spreadsheet"
)

// SpreadsheetService implements spreadsheet.v1.SpreadsheetService.
type SpreadsheetService struct {
	spreadsheets *spreadsheet.Service
	projects     *project.Service
}

// NewSpreadsheetService binds the handler to its collaborators.
func NewSpreadsheetService(spreadsheets *spreadsheet.Service, projects *project.Service) *SpreadsheetService {
	return &SpreadsheetService{spreadsheets: spreadsheets, projects: projects}
}

// List returns the spreadsheets registered in the request's project.
func (s *SpreadsheetService) List(ctx context.Context, req *connect.Request[spreadsheetv1.ListRequest]) (*connect.Response[spreadsheetv1.ListResponse], error) {
	tctx, err := scopeToProject(ctx, s.projects, req.Msg.GetProjectId())
	if err != nil {
		return nil, err
	}
	items, err := s.spreadsheets.List(tctx)
	if err != nil {
		return nil, err
	}
	resp := &spreadsheetv1.ListResponse{Spreadsheets: make([]*spreadsheetv1.Spreadsheet, 0, len(items))}
	for _, sp := range items {
		resp.Spreadsheets = append(resp.Spreadsheets, toSpreadsheetProto(sp))
	}
	return connect.NewResponse(resp), nil
}

// Create registers a Google document in the request's project, bound to one credential.
func (s *SpreadsheetService) Create(ctx context.Context, req *connect.Request[spreadsheetv1.CreateRequest]) (*connect.Response[spreadsheetv1.CreateResponse], error) {
	tctx, err := scopeToProject(ctx, s.projects, req.Msg.GetProjectId())
	if err != nil {
		return nil, err
	}
	credID, err := parseUUID("credential_id", req.Msg.GetCredentialId())
	if err != nil {
		return nil, err
	}
	sp, err := s.spreadsheets.Register(tctx, credID, req.Msg.GetGoogleFileId(), req.Msg.GetTitle())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&spreadsheetv1.CreateResponse{Spreadsheet: toSpreadsheetProto(sp)}), nil
}

// Get returns the referenced spreadsheet.
func (s *SpreadsheetService) Get(ctx context.Context, req *connect.Request[spreadsheetv1.GetRequest]) (*connect.Response[spreadsheetv1.GetResponse], error) {
	tctx, id, err := s.scoped(ctx, req.Msg.GetProjectId(), req.Msg.GetSpreadsheetId())
	if err != nil {
		return nil, err
	}
	sp, err := s.spreadsheets.ByID(tctx, id)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&spreadsheetv1.GetResponse{Spreadsheet: toSpreadsheetProto(sp)}), nil
}

// Update retitles and rebinds the referenced spreadsheet; an absent field is left alone.
func (s *SpreadsheetService) Update(ctx context.Context, req *connect.Request[spreadsheetv1.UpdateRequest]) (*connect.Response[spreadsheetv1.UpdateResponse], error) {
	tctx, id, err := s.scoped(ctx, req.Msg.GetProjectId(), req.Msg.GetSpreadsheetId())
	if err != nil {
		return nil, err
	}
	sp, err := s.spreadsheets.ByID(tctx, id)
	if err != nil {
		return nil, err
	}
	if req.Msg.Title != nil {
		sp, err = s.spreadsheets.Retitle(tctx, id, req.Msg.GetTitle())
		if err != nil {
			return nil, err
		}
	}
	if req.Msg.CredentialId != nil {
		credID, perr := parseUUID("credential_id", req.Msg.GetCredentialId())
		if perr != nil {
			return nil, perr
		}
		sp, err = s.spreadsheets.Rebind(tctx, id, credID)
		if err != nil {
			return nil, err
		}
	}
	return connect.NewResponse(&spreadsheetv1.UpdateResponse{Spreadsheet: toSpreadsheetProto(sp)}), nil
}

// Delete removes the referenced spreadsheet.
func (s *SpreadsheetService) Delete(ctx context.Context, req *connect.Request[spreadsheetv1.DeleteRequest]) (*connect.Response[spreadsheetv1.DeleteResponse], error) {
	tctx, id, err := s.scoped(ctx, req.Msg.GetProjectId(), req.Msg.GetSpreadsheetId())
	if err != nil {
		return nil, err
	}
	if err := s.spreadsheets.Delete(tctx, id); err != nil {
		return nil, err
	}
	return connect.NewResponse(&spreadsheetv1.DeleteResponse{}), nil
}

// ListTabs returns the tab titles Google reports for the referenced spreadsheet.
func (s *SpreadsheetService) ListTabs(ctx context.Context, req *connect.Request[spreadsheetv1.ListTabsRequest]) (*connect.Response[spreadsheetv1.ListTabsResponse], error) {
	tctx, id, err := s.scoped(ctx, req.Msg.GetProjectId(), req.Msg.GetSpreadsheetId())
	if err != nil {
		return nil, err
	}
	tabs, err := s.spreadsheets.ListTabs(tctx, id)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&spreadsheetv1.ListTabsResponse{Tabs: tabs}), nil
}

func (s *SpreadsheetService) scoped(ctx context.Context, projectIDRaw, idRaw string) (context.Context, uuid.UUID, error) {
	tctx, err := scopeToProject(ctx, s.projects, projectIDRaw)
	if err != nil {
		return nil, uuid.Nil, err
	}
	id, err := parseUUID("spreadsheet_id", idRaw)
	if err != nil {
		return nil, uuid.Nil, err
	}
	return tctx, id, nil
}

func toSpreadsheetProto(sp *spreadsheet.Spreadsheet) *spreadsheetv1.Spreadsheet {
	return &spreadsheetv1.Spreadsheet{
		Id:           sp.ID.String(),
		ProjectId:    sp.ProjectID.String(),
		CredentialId: sp.CredentialID.String(),
		GoogleFileId: sp.GoogleFileID,
		Title:        sp.Title,
		CreatedAt:    timestamppb.New(sp.CreatedAt),
		UpdatedAt:    timestamppb.New(sp.UpdatedAt),
	}
}
