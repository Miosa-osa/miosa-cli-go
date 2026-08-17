package miosa

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

type ForgeRepository struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Slug          string   `json:"slug"`
	DefaultBranch string   `json:"default_branch"`
	Visibility    string   `json:"visibility"`
	State         string   `json:"state"`
	CloneReady    bool     `json:"clone_ready"`
	CloneURL      *string  `json:"clone_url"`
	ProjectIDs    []string `json:"project_ids"`
	CreatedAt     string   `json:"created_at"`
	UpdatedAt     string   `json:"updated_at"`
}

type CreateForgeRepositoryInput struct {
	Name           string   `json:"name"`
	Slug           string   `json:"slug,omitempty"`
	DefaultBranch  string   `json:"default_branch,omitempty"`
	Visibility     string   `json:"visibility,omitempty"`
	ProjectIDs     []string `json:"project_ids,omitempty"`
	IdempotencyKey string   `json:"-"`
}

type UpdateForgeRepositoryInput struct {
	Name       string   `json:"name,omitempty"`
	Slug       string   `json:"slug,omitempty"`
	Visibility string   `json:"visibility,omitempty"`
	ProjectIDs []string `json:"project_ids,omitempty"`
}
type ForgeDeleteReceipt struct {
	OperationID string `json:"operation_id"`
	Replayed    bool   `json:"replayed"`
}

type ForgeService struct{ client *Client }

func (s *ForgeService) Create(ctx context.Context, input CreateForgeRepositoryInput) (*ForgeRepository, error) {
	if input.IdempotencyKey == "" {
		var keyErr error
		input.IdempotencyKey, keyErr = forgeIdempotencyKey()
		if keyErr != nil {
			return nil, keyErr
		}
	}
	body, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("marshal Forge repository: %w", err)
	}
	var out struct {
		Data ForgeRepository `json:"data"`
	}
	if err := s.send(ctx, http.MethodPost, "/forge/repositories", body, input.IdempotencyKey, &out); err != nil {
		return nil, translateForgeError(err)
	}
	return &out.Data, validateForgeRepository(&out.Data)
}

func (s *ForgeService) List(ctx context.Context) ([]ForgeRepository, error) {
	var out struct {
		Data []ForgeRepository `json:"data"`
	}
	if err := s.client.getJSON(ctx, "/forge/repositories", &out); err != nil {
		return nil, translateForgeError(err)
	}
	for i := range out.Data {
		if err := validateForgeRepository(&out.Data[i]); err != nil {
			return nil, err
		}
	}
	return out.Data, nil
}

func (s *ForgeService) Get(ctx context.Context, id string) (*ForgeRepository, error) {
	var out struct {
		Data ForgeRepository `json:"data"`
	}
	if err := s.client.getJSON(ctx, "/forge/repositories/"+url.PathEscape(id), &out); err != nil {
		return nil, translateForgeError(err)
	}
	return &out.Data, validateForgeRepository(&out.Data)
}

func (s *ForgeService) Update(ctx context.Context, id string, input UpdateForgeRepositoryInput) (*ForgeRepository, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	var out struct {
		Data ForgeRepository `json:"data"`
	}
	if err := s.send(ctx, http.MethodPatch, "/forge/repositories/"+url.PathEscape(id), body, "", &out); err != nil {
		return nil, translateForgeError(err)
	}
	return &out.Data, validateForgeRepository(&out.Data)
}

func (s *ForgeService) Delete(ctx context.Context, id string) (*ForgeDeleteReceipt, error) {
	resp, err := s.sendResponse(ctx, http.MethodDelete, "/forge/repositories/"+url.PathEscape(id), nil, "")
	if err != nil {
		return nil, translateForgeError(err)
	}
	operationID := resp.Header.Get("X-Forge-Operation-Id")
	if operationID == "" {
		return nil, fmt.Errorf("miosa: Forge delete omitted its operation receipt")
	}
	return &ForgeDeleteReceipt{OperationID: operationID, Replayed: resp.Header.Get("Idempotency-Replayed") == "true"}, nil
}

func translateForgeError(err error) error {
	if err == nil {
		return nil
	}
	base := miosaErrorDetails(err)
	if base == nil {
		return err
	}
	switch base.Code {
	case "FORGE_STORAGE_UNAVAILABLE", "FORGE_OPERATION_FAILED":
		return &ForgeStorageError{*base}
	case "INVALID_PROJECT_ATTACHMENT":
		return &ForgePolicyViolationError{*base}
	default:
		return err
	}
}

func miosaErrorDetails(err error) *MiosaError {
	switch value := err.(type) {
	case *MiosaError:
		return value
	case *AuthenticationError:
		return &value.MiosaError
	case *PermissionError:
		return &value.MiosaError
	case *NotFoundError:
		return &value.MiosaError
	case *ValidationError:
		return &value.MiosaError
	case *InsufficientCreditsError:
		return &value.MiosaError
	case *RateLimitError:
		return &value.MiosaError
	case *ServerError:
		return &value.MiosaError
	default:
		return nil
	}
}

func (s *ForgeService) send(ctx context.Context, method, path string, body []byte, idempotencyKey string, out interface{}) error {
	resp, err := s.sendResponse(ctx, method, path, body, idempotencyKey)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (s *ForgeService) sendResponse(ctx context.Context, method, path string, body []byte, idempotencyKey string) (*http.Response, error) {
	headers := http.Header{}
	if idempotencyKey != "" {
		headers.Set("Idempotency-Key", idempotencyKey)
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	return s.client.doWithHeaders(ctx, method, path, reader, headers)
}

func validateForgeRepository(repository *ForgeRepository) error {
	if repository.ID == "" || repository.Name == "" || repository.Slug == "" || repository.DefaultBranch == "" ||
		repository.CreatedAt == "" || repository.UpdatedAt == "" || repository.ProjectIDs == nil {
		return fmt.Errorf("miosa: Forge repository response did not match the published contract")
	}
	if repository.Visibility != "public" && repository.Visibility != "private" && repository.Visibility != "internal" {
		return fmt.Errorf("miosa: Forge repository response has invalid visibility")
	}
	if repository.State != "provisioning" && repository.State != "active" && repository.State != "error" && repository.State != "deletion_pending" && repository.State != "deleted" {
		return fmt.Errorf("miosa: Forge repository response has invalid state")
	}
	if repository.CloneReady != (repository.State == "active") || (repository.CloneReady && (repository.CloneURL == nil || *repository.CloneURL == "")) || (!repository.CloneReady && repository.CloneURL != nil) {
		return fmt.Errorf("miosa: Forge repository response has inconsistent clone readiness")
	}
	return nil
}

func forgeIdempotencyKey() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate Forge idempotency key: %w", err)
	}
	return fmt.Sprintf("%x", value), nil
}
