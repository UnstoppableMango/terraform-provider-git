package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/UnstoppableMango/terraform-provider-git/internal/git"
	"github.com/UnstoppableMango/terraform-provider-git/internal/git/github"
)

// Ensure the implementation satisfies the desired interfaces.
var _ resource.Resource = &gitPatchResource{}
var _ resource.ResourceWithConfigure = &gitPatchResource{}
var _ resource.ResourceWithConfigValidators = &gitPatchResource{}

// gitPatchResource manages the identity and resolved content of a single
// patch: a local file, inline diff content, or a remote host source (e.g. a
// GitHub PR/commit). It does not clone, apply, commit, or push; that is
// git_branch's responsibility once it exists.
type gitPatchResource struct {
	client git.Client
	github github.Client
}

// gitPatchResourceModel describes the resource's data model.
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

// NewGitPatchResource creates a new instance of the git_patch resource.
func NewGitPatchResource() resource.Resource {
	return &gitPatchResource{}
}

func (r *gitPatchResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_patch"
}

func (r *gitPatchResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	data, ok := req.ProviderData.(*providerData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *providerData, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}

	r.client = data.GitClient
	r.github = data.GithubClient
}

func (r *gitPatchResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A single patch: a local file, inline diff content, or a remote host source (e.g. a GitHub PR/commit). Exactly one of `content`, `file`, or `github` must be set. Does not clone, apply, commit, or push; that is `git_branch`'s responsibility.",
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
				MarkdownDescription: "Path to a local patch file, read on create/read.",
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
// set on the resource config.
func (r *gitPatchResource) ConfigValidators(_ context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		resourcevalidator.ExactlyOneOf(
			path.MatchRoot("content"),
			path.MatchRoot("file"),
			path.MatchRoot("github"),
		),
	}
}

// githubAuthFromModel converts the resource's optional auth model into a
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
func (r *gitPatchResource) resolve(ctx context.Context, model *gitPatchResourceModel) error {
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
			resolution, err = r.github.ResolvePR(ctx, model.Github.Repository.ValueString(), model.Github.Pr.ValueInt64(), auth)
		} else {
			resolution, err = r.github.ResolveCommit(ctx, model.Github.Repository.ValueString(), model.Github.Commit.ValueString(), auth)
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

func (r *gitPatchResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var model gitPatchResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.resolve(ctx, &model); err != nil {
		resp.Diagnostics.AddError("Error Resolving Patch", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}

func (r *gitPatchResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var model gitPatchResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.resolve(ctx, &model); err != nil {
		resp.Diagnostics.AddError("Error Resolving Patch", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}

func (r *gitPatchResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var model gitPatchResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.resolve(ctx, &model); err != nil {
		resp.Diagnostics.AddError("Error Resolving Patch", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}

func (r *gitPatchResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
	// No external side effects: the resource never clones, applies, or
	// pushes anything. The framework removes the resource from state
	// automatically after a Delete that adds no diagnostics errors.
}
