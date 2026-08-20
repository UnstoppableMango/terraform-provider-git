package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/UnstoppableMango/terraform-provider-git/internal/git/github"
	"github.com/UnstoppableMango/terraform-provider-git/internal/git/gitlab"
)

// Ensure the implementation satisfies the desired interfaces.
var _ datasource.DataSource = &gitPatchDataSource{}
var _ datasource.DataSourceWithConfigure = &gitPatchDataSource{}
var _ datasource.DataSourceWithConfigValidators = &gitPatchDataSource{}

// gitPatchDataSource resolves the identity and content of a single patch: a
// local file, inline diff content, or a remote host source (e.g. a GitHub
// PR/commit or a GitLab MR/commit). It does not clone, apply, commit, or
// push; that is git_branch's responsibility once it exists.
type gitPatchDataSource struct {
	github       github.Client
	gitlab       gitlab.Client
	defaultToken string
}

// gitPatchResourceModel describes the data source's data model.
type gitPatchResourceModel struct {
	Id      types.String            `tfsdk:"id"`
	Content types.String            `tfsdk:"content"`
	File    types.String            `tfsdk:"file"`
	Diff    types.String            `tfsdk:"diff"`
	Github  *gitPatchGithubModel    `tfsdk:"github"`
	Gitlab  *gitPatchGitlabModel    `tfsdk:"gitlab"`
	Auth    *gitRepositoryAuthModel `tfsdk:"auth"`
}

// gitPatchGithubModel describes the github nested attribute data model.
type gitPatchGithubModel struct {
	Repository types.String `tfsdk:"repository"`
	Pr         types.Int64  `tfsdk:"pr"`
	Commit     types.String `tfsdk:"commit"`
	Sha        types.String `tfsdk:"sha"`
}

// gitPatchGitlabModel describes the gitlab nested attribute data model.
type gitPatchGitlabModel struct {
	Project types.String `tfsdk:"project"`
	Mr      types.Int64  `tfsdk:"mr"`
	Commit  types.String `tfsdk:"commit"`
	Sha     types.String `tfsdk:"sha"`
}

// NewGitPatchDataSource creates a new instance of the git_patch data source.
func NewGitPatchDataSource() datasource.DataSource {
	return &gitPatchDataSource{}
}

func (d *gitPatchDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_patch"
}

func (d *gitPatchDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	data, ok := req.ProviderData.(*providerData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *providerData, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}

	d.github = data.GithubClient
	d.gitlab = data.GitlabClient
	d.defaultToken = data.DefaultToken
}

func (d *gitPatchDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Resolves a single patch: a local file, inline diff content, or a remote host source (e.g. a GitHub PR/commit or a GitLab MR/commit). Exactly one of `content`, `file`, `github`, or `gitlab` must be set. Does not clone, apply, commit, or push; that is `git_branch`'s responsibility.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Stable identifier for the patch: the hex-encoded sha256 of the resolved `diff`.",
			},
			"content": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Inline unified diff content.",
			},
			"file": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Path to a local patch file, read on refresh.",
			},
			"diff": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resolved unified diff content, from whichever of `content`, `file`, or `github` was set.",
			},
			"github": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Resolves the patch from a GitHub pull request or commit. Exactly one of `pr` or `commit` must be set.",
				Attributes: map[string]schema.Attribute{
					"repository": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "Repository the pull request or commit belongs to, in `owner/name` form.",
					},
					"pr": schema.Int64Attribute{
						Optional:            true,
						MarkdownDescription: "Pull request number to resolve the patch from.",
						Validators: []validator.Int64{
							int64validator.ExactlyOneOf(
								path.MatchRelative().AtParent().AtName("pr"),
								path.MatchRelative().AtParent().AtName("commit"),
							),
						},
					},
					"commit": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "Commit sha to resolve the patch from.",
					},
					"sha": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Resolved commit sha (the pull request's head commit, or `commit` itself).",
					},
				},
			},
			"gitlab": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Resolves the patch from a GitLab merge request or commit. Exactly one of `mr` or `commit` must be set.",
				Attributes: map[string]schema.Attribute{
					"project": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "Project the merge request or commit belongs to, as a GitLab project path (e.g. `group/project`) or numeric ID.",
					},
					"mr": schema.Int64Attribute{
						Optional:            true,
						MarkdownDescription: "Merge request IID to resolve the patch from.",
						Validators: []validator.Int64{
							int64validator.ExactlyOneOf(
								path.MatchRelative().AtParent().AtName("mr"),
								path.MatchRelative().AtParent().AtName("commit"),
							),
						},
					},
					"commit": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "Commit sha to resolve the patch from.",
					},
					"sha": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Resolved commit sha (the merge request's head commit, or `commit` itself).",
					},
				},
			},
			"auth": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Authentication details used to call the GitHub or GitLab API when `github` or `gitlab` is set.",
				Attributes: map[string]schema.Attribute{
					"token": schema.StringAttribute{
						Optional:            true,
						Sensitive:           true,
						MarkdownDescription: "Token used to authenticate with the GitHub or GitLab API.",
					},
				},
			},
		},
	}
}

// ConfigValidators enforces that exactly one of content, file, github, or
// gitlab is set on the data source config.
func (d *gitPatchDataSource) ConfigValidators(_ context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		datasourcevalidator.ExactlyOneOf(
			path.MatchRoot("content"),
			path.MatchRoot("file"),
			path.MatchRoot("github"),
			path.MatchRoot("gitlab"),
		),
	}
}

// errGithubNotConfigured is returned by resolve when a github source is
// configured but the provider has no github client. Callers use errors.Is
// to distinguish this provider-configuration problem from errors caused by
// the github attribute's own content, which should instead be reported as
// an attribute-scoped diagnostic.
var errGithubNotConfigured = errors.New("github client not configured")

// errGitlabNotConfigured mirrors errGithubNotConfigured for the gitlab source.
var errGitlabNotConfigured = errors.New("gitlab client not configured")

// resolve derives diff (and, when github is set, github.sha) from whichever
// of content, file, or github is set on model, and computes id as the hex
// sha256 digest of the resolved diff.
func (d *gitPatchDataSource) resolve(ctx context.Context, model *gitPatchResourceModel) error {
	var diff string

	switch {
	case !model.Content.IsNull() && !model.Content.IsUnknown():
		diff = model.Content.ValueString()
	case !model.File.IsNull() && !model.File.IsUnknown():
		content, err := os.ReadFile(model.File.ValueString())
		if err != nil {
			return fmt.Errorf("reading file %q: %w", model.File.ValueString(), err)
		}
		diff = string(content)
	case model.Github != nil:
		if d.github == nil {
			return errGithubNotConfigured
		}

		token := tokenFromModel(model.Auth, d.defaultToken)

		var resolution github.Resolution
		var err error
		if !model.Github.Pr.IsNull() && !model.Github.Pr.IsUnknown() {
			resolution, err = d.github.ResolvePR(ctx, model.Github.Repository.ValueString(), model.Github.Pr.ValueInt64(), token)
		} else {
			resolution, err = d.github.ResolveCommit(ctx, model.Github.Repository.ValueString(), model.Github.Commit.ValueString(), token)
		}
		if err != nil {
			return fmt.Errorf("resolving github source: %w", err)
		}

		diff = resolution.Diff
		model.Github.Sha = types.StringValue(resolution.SHA)
	case model.Gitlab != nil:
		if d.gitlab == nil {
			return errGitlabNotConfigured
		}

		token := tokenFromModel(model.Auth, d.defaultToken)

		var resolution gitlab.Resolution
		var err error
		if !model.Gitlab.Mr.IsNull() && !model.Gitlab.Mr.IsUnknown() {
			resolution, err = d.gitlab.ResolveMR(ctx, model.Gitlab.Project.ValueString(), model.Gitlab.Mr.ValueInt64(), token)
		} else {
			resolution, err = d.gitlab.ResolveCommit(ctx, model.Gitlab.Project.ValueString(), model.Gitlab.Commit.ValueString(), token)
		}
		if err != nil {
			return fmt.Errorf("resolving gitlab source: %w", err)
		}

		diff = resolution.Diff
		model.Gitlab.Sha = types.StringValue(resolution.SHA)
	default:
		return fmt.Errorf("exactly one of content, file, github, or gitlab must be set")
	}

	sum := sha256.Sum256([]byte(diff))

	model.Diff = types.StringValue(diff)
	model.Id = types.StringValue(hex.EncodeToString(sum[:]))

	return nil
}

func (d *gitPatchDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var model gitPatchResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := d.resolve(ctx, &model); err != nil {
		switch {
		case !model.File.IsNull() && !model.File.IsUnknown():
			resp.Diagnostics.AddAttributeError(path.Root("file"), "Unable to Resolve Patch", err.Error())
		case model.Github != nil && !errors.Is(err, errGithubNotConfigured):
			resp.Diagnostics.AddAttributeError(path.Root("github"), "Unable to Resolve Patch", err.Error())
		case model.Gitlab != nil && !errors.Is(err, errGitlabNotConfigured):
			resp.Diagnostics.AddAttributeError(path.Root("gitlab"), "Unable to Resolve Patch", err.Error())
		default:
			resp.Diagnostics.AddError("Unable to Resolve Patch", err.Error())
		}
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
}
