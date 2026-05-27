package miosa

import (
	"context"
	"fmt"
	"strconv"
)

// WorkspacesService manages workspace resources on a Client.
type WorkspacesService struct {
	client *Client
}

func (s *WorkspacesService) base() string { return "/workspaces" }

// Create provisions a new workspace.
func (s *WorkspacesService) Create(ctx context.Context, input CreateWorkspaceInput) (*Workspace, error) {
	const op = "WorkspacesService.Create"
	var out Workspace
	if err := s.client.postJSON(ctx, s.base(), input, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return &out, nil
}

// List returns a paginated list of workspaces.
func (s *WorkspacesService) List(ctx context.Context, input ListWorkspacesInput) (*WorkspaceListResponse, error) {
	const op = "WorkspacesService.List"
	params := map[string]string{}
	if input.Page > 0 {
		params["page"] = strconv.Itoa(input.Page)
	}
	if input.PerPage > 0 {
		params["per_page"] = strconv.Itoa(input.PerPage)
	}
	var out WorkspaceListResponse
	if err := s.client.getJSON(ctx, s.base()+buildQuery(params), &out); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return &out, nil
}

// Get fetches a single workspace by ID.
func (s *WorkspacesService) Get(ctx context.Context, id string) (*Workspace, error) {
	const op = "WorkspacesService.Get"
	var out Workspace
	if err := s.client.getJSON(ctx, s.base()+"/"+id, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return &out, nil
}

// Update modifies workspace metadata.
func (s *WorkspacesService) Update(ctx context.Context, id string, input UpdateWorkspaceInput) (*Workspace, error) {
	const op = "WorkspacesService.Update"
	var out Workspace
	if err := s.client.patchJSON(ctx, s.base()+"/"+id, input, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return &out, nil
}

// Delete permanently destroys a workspace.
func (s *WorkspacesService) Delete(ctx context.Context, id string) error {
	const op = "WorkspacesService.Delete"
	if err := s.client.deleteJSON(ctx, s.base()+"/"+id, nil); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}

// ListComputers returns all computers associated with a workspace.
func (s *WorkspacesService) ListComputers(ctx context.Context, workspaceID string) (*ComputerListResponse, error) {
	const op = "WorkspacesService.ListComputers"
	var out ComputerListResponse
	if err := s.client.getJSON(ctx, fmt.Sprintf("%s/%s/computers", s.base(), workspaceID), &out); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return &out, nil
}
