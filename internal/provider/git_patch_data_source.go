package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/UnstoppableMango/terraform-provider-git/internal/git"
	"github.com/UnstoppableMango/terraform-provider-git/internal/git/github"
)

// Ensure the implementation satisfies the desired interfaces.
var _ datasource.DataSource = &gitPatchDataSource{}
var _ datasource.DataSourceWithConfigure = &gitPatchDataSource{}
var _ datasource.DataSourceWithConfigValidators = &gitPatchDataSource{}

// gitPatchDataSource resolves the identity and content of a single patch: a
// local file, inline diff content, or a remote host source (e.g. a GitHub
// PR/commit). It does not clone, apply, commit, or push; that is
// git_branch's responsibility once it exists.
type gitPatchDataSource struct {
	client git.Client
	github github.Client
}

// gitPatchResourceModel describes the data source's data model.
type gitPatchResourceModel struct {
	Id      types.String            `tfsdk:"id"`
	Content types.String            `tfsdk:"content"`
	File    types.String            `tfsdk:"file"`
	Diff    types.String            `tfsdk:"diff"`
	Github  *gitPatchGithubModel    `tfsdk:"github"`
	Auth    *gitRepositoryAuthModel `tfsdk:"auth"`
}

// gitPatchGithubModel describes the github nested attribute data model.
type gitPatchGithubModel struct {
	Repository types.String `tfsdk:"repository"`
	Pr         types.Int64  `tfsdk:"pr"`
	Commit     types.String `tfsdk:"commit"`
	Sha        types.String `tfsdk:"sha"`
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

	d.client = data.GitClient
	d.github = data.GithubClient
}

func (d *gitPatchDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Resolves a single patch: a local file, inline diff content, or a remote host source (e.g. a GitHub PR/commit). Exactly one of `content`, `file`, or `github` must be set. Does not clone, apply, commit, or push; that is `git_branch`'s responsibility.",
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
			"auth": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Authentication details used to call the GitHub API when `github` is set.",
				Attributes: map[string]schema.Attribute{
					"token": schema.StringAttribute{
						Optional:            true,
						Sensitive:           true,
						MarkdownDescription: "Token used to authenticate with the GitHub API.",
					},
				},
			},
		},
	}
}

// ConfigValidators enforces that exactly one of content, file, or github is
// set on the data source config.
func (d *gitPatchDataSource) ConfigValidators(_ context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		datasourcevalidator.ExactlyOneOf(
			path.MatchRoot("content"),
			path.MatchRoot("file"),
			path.MatchRoot("github"),
		),
	}
}

// githubAuthFromModel converts the data source's optional auth model into a
// github.Auth, treating a nil or unset token as unauthenticated.
func githubAuthFromModel(m *gitRepositoryAuthModel) github.Auth {
	if m == nil || m.Token.IsNull() || m.Token.IsUnknown() {
		return github.Auth{}
	}

	return github.Auth{Token: m.Token.ValueString()}
}

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
		auth := githubAuthFromModel(model.Auth)

		var resolution github.Resolution
		var err error
		if !model.Github.Pr.IsNull() && !model.Github.Pr.IsUnknown() {
			resolution, err = d.github.ResolvePR(ctx, model.Github.Repository.ValueString(), model.Github.Pr.ValueInt64(), auth)
		} else {
			resolution, err = d.github.ResolveCommit(ctx, model.Github.Repository.ValueString(), model.Github.Commit.ValueString(), auth)
		}
		if err != nil {
			return fmt.Errorf("resolving github source: %w", err)
		}

		diff = resolution.Diff
		model.Github.Sha = types.StringValue(resolution.SHA)
	default:
		return fmt.Errorf("exactly one of content, file, or github must be set")
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
		resp.Diagnostics.AddError("Unable to Resolve Patch", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
}
