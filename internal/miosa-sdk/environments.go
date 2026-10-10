package miosa

import (
	"context"
	"fmt"
	"net/url"
)

// Wire contract: miosa-compute docs/api/environments.md (branch feat/environments).
// Every path below is relative to the API base URL (".../api/v1").
// This file is the only place that knows those paths and field names.

// EnvironmentVariable is a variable as listed on an environment.
// The API never returns plaintext here; Preview is masked.
type EnvironmentVariable struct {
	Name    string `json:"name"`
	Preview string `json:"preview"`
}

// EnvironmentSecretFile is a secret file as listed on an environment (no contents).
type EnvironmentSecretFile struct {
	Path      string `json:"path"`
	SizeBytes int    `json:"size_bytes"`
}

// EnvironmentRepository is a repository attached to an environment.
type EnvironmentRepository struct {
	Source        string `json:"source,omitempty"`
	Repo          string `json:"repo"`
	BaseBranch    string `json:"base_branch,omitempty"`
	SetupScript   string `json:"setup_script,omitempty"`
	SetupBlocking bool   `json:"setup_blocking"`
	Path          string `json:"path,omitempty"`
}

// EnvironmentEffective is what a machine actually receives.
type EnvironmentEffective struct {
	PassGithub             bool `json:"pass_github"`
	PassSecrets            bool `json:"pass_secrets"`
	PassSandboxCredentials bool `json:"pass_sandbox_credentials"`
	PassAgentsCredentials  bool `json:"pass_agents_credentials"`
}

// Environment is a named template that new machines inherit.
type Environment struct {
	ID                     string                  `json:"id"`
	Name                   string                  `json:"name"`
	WorkspaceID            string                  `json:"workspace_id,omitempty"`
	IsDefault              bool                    `json:"is_default"`
	SafeForThirdParties    bool                    `json:"safe_for_third_parties"`
	PassGithub             bool                    `json:"pass_github"`
	PassSecrets            bool                    `json:"pass_secrets"`
	PassSandboxCredentials bool                    `json:"pass_sandbox_credentials"`
	PassAgentsCredentials  bool                    `json:"pass_agents_credentials"`
	Effective              EnvironmentEffective    `json:"effective"`
	LatestVersion          int                     `json:"latest_version"`
	VersionCount           int                     `json:"version_count"`
	PinnedMachineCount     int                     `json:"pinned_machine_count"`
	PinnedSandboxCount     int                     `json:"pinned_sandbox_count"`
	PinnedComputerCount    int                     `json:"pinned_computer_count"`
	OutdatedMachineCount   int                     `json:"outdated_machine_count"`
	Variables              []EnvironmentVariable   `json:"variables"`
	SecretFiles            []EnvironmentSecretFile `json:"secret_files"`
	Repositories           []EnvironmentRepository `json:"repositories"`
	CreatedAt              string                  `json:"created_at,omitempty"`
	UpdatedAt              string                  `json:"updated_at,omitempty"`
}

// EnvironmentVersion is one immutable version of an environment. It never
// carries values, only names and paths.
type EnvironmentVersion struct {
	Version                int                     `json:"version"`
	CreatedAt              string                  `json:"created_at,omitempty"`
	CreatedBy              string                  `json:"created_by,omitempty"`
	PinnedMachineCount     int                     `json:"pinned_machine_count"`
	PinnedSandboxCount     int                     `json:"pinned_sandbox_count"`
	PinnedComputerCount    int                     `json:"pinned_computer_count"`
	SafeForThirdParties    bool                    `json:"safe_for_third_parties"`
	PassGithub             bool                    `json:"pass_github"`
	PassSecrets            bool                    `json:"pass_secrets"`
	PassSandboxCredentials bool                    `json:"pass_sandbox_credentials"`
	PassAgentsCredentials  bool                    `json:"pass_agents_credentials"`
	VariableNames          []string                `json:"variable_names"`
	SecretFilePaths        []string                `json:"secret_file_paths"`
	Repositories           []EnvironmentRepository `json:"repositories"`
}

// EnvironmentList is the response of GET /environments.
type EnvironmentList struct {
	Environments         []Environment `json:"environments"`
	DefaultEnvironmentID string        `json:"default_environment_id"`
}

// CreateEnvironmentInput is the body of POST /environments.
type CreateEnvironmentInput struct {
	Name                string `json:"name"`
	WorkspaceID         string `json:"workspace_id,omitempty"`
	SafeForThirdParties *bool  `json:"safe_for_third_parties,omitempty"`
}

// UpdateEnvironmentInput is the body of PATCH /environments/:id.
// Nil fields are omitted, so only what you set changes.
// Variables, secret files and repositories are deliberately absent: the API
// treats them as full replacements, and the granular calls cannot drop data.
type UpdateEnvironmentInput struct {
	SafeForThirdParties    *bool `json:"safe_for_third_parties,omitempty"`
	PassGithub             *bool `json:"pass_github,omitempty"`
	PassSecrets            *bool `json:"pass_secrets,omitempty"`
	PassSandboxCredentials *bool `json:"pass_sandbox_credentials,omitempty"`
	PassAgentsCredentials  *bool `json:"pass_agents_credentials,omitempty"`
}

// AddRepositoryInput is the body of POST /environments/:id/repositories.
type AddRepositoryInput struct {
	Repo          string `json:"repo"`
	Source        string `json:"source,omitempty"`
	BaseBranch    string `json:"base_branch,omitempty"`
	SetupScript   string `json:"setup_script,omitempty"`
	SetupBlocking *bool  `json:"setup_blocking,omitempty"`
}

// EnvironmentUpgradedMachine is one machine moved by an upgrade.
type EnvironmentUpgradedMachine struct {
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	FromVersion int    `json:"from_version"`
	ToVersion   int    `json:"to_version"`
	Status      string `json:"status"`
}

// EnvironmentUpgradeResult is the response of POST /environments/:id/upgrade.
type EnvironmentUpgradeResult struct {
	EnvironmentID string                       `json:"environment_id"`
	LatestVersion int                          `json:"latest_version"`
	Machines      []EnvironmentUpgradedMachine `json:"machines"`
}

// MachineEnvironment is the environment block carried by every sandbox and
// computer JSON. All fields are empty for a machine that predates environments.
type MachineEnvironment struct {
	Environment                 string `json:"environment,omitempty"`
	EnvironmentID               string `json:"environment_id,omitempty"`
	EnvironmentVersion          int    `json:"environment_version,omitempty"`
	EnvironmentLatestVersion    int    `json:"environment_latest_version,omitempty"`
	EnvironmentUpgradeAvailable bool   `json:"environment_upgrade_available,omitempty"`
	EnvironmentProtected        bool   `json:"environment_protected,omitempty"`
	EnvironmentStatus           string `json:"environment_status,omitempty"`
	EnvironmentError            string `json:"environment_error,omitempty"`
	SetupStatus                 string `json:"setup_status,omitempty"`
	SetupError                  string `json:"setup_error,omitempty"`
}

// EnvironmentsService manages environments. Accessed via Client.Environments.
//
// Environments belong to a workspace, or to the organization when no
// workspace applies. ForWorkspace returns a view scoped to one workspace;
// the unscoped service lets the API resolve the scope (a workspace-bound key's
// workspace, else organization level).
type EnvironmentsService struct {
	client      *Client
	workspaceID string
}

// ForWorkspace returns the service scoped to a workspace id. An empty id
// returns the unscoped service.
func (s *EnvironmentsService) ForWorkspace(workspaceID string) *EnvironmentsService {
	return &EnvironmentsService{client: s.client, workspaceID: workspaceID}
}

// WorkspaceID is the workspace this service is scoped to, or "".
func (s *EnvironmentsService) WorkspaceID() string { return s.workspaceID }

// at builds a request path with the workspace scope and any extra query.
func (s *EnvironmentsService) at(path string, query map[string]string) string {
	q := map[string]string{}
	for k, v := range query {
		q[k] = v
	}
	if s.workspaceID != "" {
		q["workspace_id"] = s.workspaceID
	}
	return path + buildQuery(q)
}

func envPath(idOrName string, parts ...string) string {
	p := "/environments/" + url.PathEscape(idOrName)
	for _, part := range parts {
		p += "/" + part
	}
	return p
}

type environmentEnvelope struct {
	Environment Environment `json:"environment"`
}

// List returns every environment, default first.
func (s *EnvironmentsService) List(ctx context.Context) (*EnvironmentList, error) {
	var out EnvironmentList
	if err := s.client.getJSON(ctx, s.at("/environments", nil), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Get fetches one environment by name or id.
func (s *EnvironmentsService) Get(ctx context.Context, idOrName string) (*Environment, error) {
	var out environmentEnvelope
	if err := s.client.getJSON(ctx, s.at(envPath(idOrName), nil), &out); err != nil {
		return nil, err
	}
	return &out.Environment, nil
}

// Default returns the default environment.
func (s *EnvironmentsService) Default(ctx context.Context) (*Environment, error) {
	list, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range list.Environments {
		e := &list.Environments[i]
		if e.ID == list.DefaultEnvironmentID || e.IsDefault {
			return e, nil
		}
	}
	return nil, fmt.Errorf("no default environment returned by the API")
}

// Create makes a new environment at version 1.
func (s *EnvironmentsService) Create(ctx context.Context, in CreateEnvironmentInput) (*Environment, error) {
	if in.WorkspaceID == "" {
		in.WorkspaceID = s.workspaceID
	}
	var out environmentEnvelope
	if err := s.client.postJSON(ctx, s.at("/environments", nil), in, &out); err != nil {
		return nil, err
	}
	return &out.Environment, nil
}

// Update changes toggles. A change to a toggle mints a new version.
func (s *EnvironmentsService) Update(ctx context.Context, idOrName string, in UpdateEnvironmentInput) (*Environment, error) {
	var out environmentEnvelope
	if err := s.client.patchJSON(ctx, s.at(envPath(idOrName), nil), in, &out); err != nil {
		return nil, err
	}
	return &out.Environment, nil
}

// Rename renames an environment; machines stay pinned.
func (s *EnvironmentsService) Rename(ctx context.Context, idOrName, newName string) (*Environment, error) {
	var out environmentEnvelope
	if err := s.client.postJSON(ctx, s.at(envPath(idOrName, "rename"), nil), map[string]string{"name": newName}, &out); err != nil {
		return nil, err
	}
	return &out.Environment, nil
}

// SetDefault makes the environment the default.
func (s *EnvironmentsService) SetDefault(ctx context.Context, idOrName string) (*Environment, error) {
	var out environmentEnvelope
	if err := s.client.postJSON(ctx, s.at(envPath(idOrName, "default"), nil), nil, &out); err != nil {
		return nil, err
	}
	return &out.Environment, nil
}

// Delete soft-deletes an environment. The default cannot be deleted.
func (s *EnvironmentsService) Delete(ctx context.Context, idOrName string) error {
	return s.client.deleteJSON(ctx, s.at(envPath(idOrName), nil), nil)
}

// SetVariable sets or replaces one variable without touching the others.
func (s *EnvironmentsService) SetVariable(ctx context.Context, idOrName, name, value string) error {
	return s.client.putJSON(ctx, s.at(envPath(idOrName, "variables", url.PathEscape(name)), nil), map[string]string{"value": value}, nil)
}

// DeleteVariable removes one variable.
func (s *EnvironmentsService) DeleteVariable(ctx context.Context, idOrName, name string) error {
	return s.client.deleteJSON(ctx, s.at(envPath(idOrName, "variables", url.PathEscape(name)), nil), nil)
}

// RevealVariable returns the plaintext value of one variable (audit-logged).
func (s *EnvironmentsService) RevealVariable(ctx context.Context, idOrName, name string) (string, error) {
	var out struct {
		Value string `json:"value"`
	}
	if err := s.client.getJSON(ctx, s.at(envPath(idOrName, "variables", url.PathEscape(name), "reveal"), nil), &out); err != nil {
		return "", err
	}
	return out.Value, nil
}

// SetSecretFile writes one secret file by path, relative to the machine home.
func (s *EnvironmentsService) SetSecretFile(ctx context.Context, idOrName, path, contents string) error {
	return s.client.putJSON(ctx, s.at(envPath(idOrName, "files"), nil), map[string]string{"path": path, "contents": contents}, nil)
}

// DeleteSecretFile removes one secret file.
func (s *EnvironmentsService) DeleteSecretFile(ctx context.Context, idOrName, path string) error {
	return s.client.deleteJSON(ctx, s.at(envPath(idOrName, "files"), map[string]string{"path": path}), nil)
}

// AddRepository attaches (or replaces the settings of) a repository.
func (s *EnvironmentsService) AddRepository(ctx context.Context, idOrName string, in AddRepositoryInput) error {
	return s.client.postJSON(ctx, s.at(envPath(idOrName, "repositories"), nil), in, nil)
}

// RemoveRepository detaches a repository.
func (s *EnvironmentsService) RemoveRepository(ctx context.Context, idOrName, repo string) error {
	return s.client.deleteJSON(ctx, s.at(envPath(idOrName, "repositories"), map[string]string{"repo": repo}), nil)
}

// Versions lists every version, newest first.
func (s *EnvironmentsService) Versions(ctx context.Context, idOrName string) ([]EnvironmentVersion, error) {
	var out struct {
		Versions []EnvironmentVersion `json:"versions"`
	}
	if err := s.client.getJSON(ctx, s.at(envPath(idOrName, "versions"), nil), &out); err != nil {
		return nil, err
	}
	return out.Versions, nil
}

// Upgrade moves machines onto the environment's latest version. With no
// machine ids it moves every live machine that is behind. Secrets the new
// version withholds are deleted from the machines; this cannot be undone.
func (s *EnvironmentsService) Upgrade(ctx context.Context, idOrName string, machineIDs []string) (*EnvironmentUpgradeResult, error) {
	var body interface{}
	if len(machineIDs) > 0 {
		body = map[string][]string{"machine_ids": machineIDs}
	}
	var out EnvironmentUpgradeResult
	if err := s.client.postJSON(ctx, s.at(envPath(idOrName, "upgrade"), nil), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
