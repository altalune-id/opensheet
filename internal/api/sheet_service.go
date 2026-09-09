package api

import (
	"context"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	sheetv1 "altalune.id/opensheet/gen/go/sheet/v1"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/sheet"
)

// SheetService implements sheet.v1.SheetService.
type SheetService struct {
	sheets   *sheet.Service
	projects *project.Service
}

// NewSheetService binds the handler to its collaborators.
func NewSheetService(sheets *sheet.Service, projects *project.Service) *SheetService {
	return &SheetService{sheets: sheets, projects: projects}
}

// List returns the sheets published in the request's project.
func (s *SheetService) List(ctx context.Context, req *connect.Request[sheetv1.ListRequest]) (*connect.Response[sheetv1.ListResponse], error) {
	tctx, err := scopeToProject(ctx, s.projects, req.Msg.GetProjectId())
	if err != nil {
		return nil, err
	}
	items, err := s.sheets.List(tctx)
	if err != nil {
		return nil, err
	}
	resp := &sheetv1.ListResponse{Sheets: make([]*sheetv1.Sheet, 0, len(items))}
	for _, sh := range items {
		resp.Sheets = append(resp.Sheets, toSheetProto(sh))
	}
	return connect.NewResponse(resp), nil
}

// Create publishes a tab of the referenced spreadsheet under a per-project slug.
func (s *SheetService) Create(ctx context.Context, req *connect.Request[sheetv1.CreateRequest]) (*connect.Response[sheetv1.CreateResponse], error) {
	tctx, err := scopeToProject(ctx, s.projects, req.Msg.GetProjectId())
	if err != nil {
		return nil, err
	}
	sprdID, err := parseUUID("spreadsheet_id", req.Msg.GetSpreadsheetId())
	if err != nil {
		return nil, err
	}
	sh, err := s.sheets.Create(tctx, sheet.CreateRequest{
		SpreadsheetID: sprdID,
		Tab:           req.Msg.GetTab(),
		Slug:          req.Msg.GetSlug(),
		Visibility:    sheet.Visibility(req.Msg.GetVisibility()),
		CacheTTL:      req.Msg.GetCacheTtl().AsDuration(),
		Writable:      req.Msg.GetWritable(),
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&sheetv1.CreateResponse{Sheet: toSheetProto(sh)}), nil
}

// Get returns the referenced sheet.
func (s *SheetService) Get(ctx context.Context, req *connect.Request[sheetv1.GetRequest]) (*connect.Response[sheetv1.GetResponse], error) {
	tctx, id, err := s.scoped(ctx, req.Msg.GetProjectId(), req.Msg.GetSheetId())
	if err != nil {
		return nil, err
	}
	sh, err := s.sheets.ByID(tctx, id)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&sheetv1.GetResponse{Sheet: toSheetProto(sh)}), nil
}

// Update changes the tab, visibility, cache TTL and writable flag of the referenced sheet; an absent field is left alone.
func (s *SheetService) Update(ctx context.Context, req *connect.Request[sheetv1.UpdateRequest]) (*connect.Response[sheetv1.UpdateResponse], error) {
	tctx, id, err := s.scoped(ctx, req.Msg.GetProjectId(), req.Msg.GetSheetId())
	if err != nil {
		return nil, err
	}
	in := sheet.UpdateInput{Tab: req.Msg.Tab}
	if req.Msg.Visibility != nil {
		vis := sheet.Visibility(req.Msg.GetVisibility())
		in.Visibility = &vis
	}
	if req.Msg.GetCacheTtl() != nil {
		ttl := req.Msg.GetCacheTtl().AsDuration()
		in.CacheTTL = &ttl
	}
	in.Writable = req.Msg.Writable
	sh, err := s.sheets.Update(tctx, id, in)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&sheetv1.UpdateResponse{Sheet: toSheetProto(sh)}), nil
}

// Delete unpublishes the referenced sheet.
func (s *SheetService) Delete(ctx context.Context, req *connect.Request[sheetv1.DeleteRequest]) (*connect.Response[sheetv1.DeleteResponse], error) {
	tctx, id, err := s.scoped(ctx, req.Msg.GetProjectId(), req.Msg.GetSheetId())
	if err != nil {
		return nil, err
	}
	if err := s.sheets.Delete(tctx, id); err != nil {
		return nil, err
	}
	return connect.NewResponse(&sheetv1.DeleteResponse{}), nil
}

// PurgeCache drops every cached snapshot of the referenced sheet.
func (s *SheetService) PurgeCache(ctx context.Context, req *connect.Request[sheetv1.PurgeCacheRequest]) (*connect.Response[sheetv1.PurgeCacheResponse], error) {
	tctx, id, err := s.scoped(ctx, req.Msg.GetProjectId(), req.Msg.GetSheetId())
	if err != nil {
		return nil, err
	}
	if err := s.sheets.PurgeCache(tctx, id); err != nil {
		return nil, err
	}
	return connect.NewResponse(&sheetv1.PurgeCacheResponse{}), nil
}

func (s *SheetService) scoped(ctx context.Context, projectIDRaw, idRaw string) (context.Context, uuid.UUID, error) {
	tctx, err := scopeToProject(ctx, s.projects, projectIDRaw)
	if err != nil {
		return nil, uuid.Nil, err
	}
	id, err := parseUUID("sheet_id", idRaw)
	if err != nil {
		return nil, uuid.Nil, err
	}
	return tctx, id, nil
}

func toSheetProto(sh *sheet.Sheet) *sheetv1.Sheet {
	return &sheetv1.Sheet{
		Id:            sh.ID.String(),
		ProjectId:     sh.ProjectID.String(),
		SpreadsheetId: sh.SpreadsheetID.String(),
		Tab:           sh.Tab,
		Slug:          sh.Slug,
		Visibility:    string(sh.Visibility),
		CacheTtl:      durationpb.New(sh.CacheTTL),
		Writable:      sh.Writable,
		CreatedAt:     timestamppb.New(sh.CreatedAt),
		UpdatedAt:     timestamppb.New(sh.UpdatedAt),
	}
}
