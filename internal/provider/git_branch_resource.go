package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/UnstoppableMango/terraform-provider-git/internal/git"
)

// Ensure the implementation satisfies the desired interfaces.
var _ resource.Resource = &gitBranchResource{}
var _ resource.ResourceWithConfigure = &gitBranchResource{}
var _ resource.ResourceWithImportState = &gitBranchResource{}

// gitBranchResource tracks a branch within a repository: the base ref it
// follows, and the ordered patch stack applied on top of it. When patches
// are set, Create/Update apply them via the configured git.Client and
// force-push the result to the branch on the remote.
type gitBranchResource struct {
	client git.Client
}

// gitBranchRepositoryModel describes the repository nested attribute data
// model. Shaped like gitRepositoryDataSourceModel (minus id) so it can be
// populated wholesale from a git_repository data source.
type gitBranchRepositoryModel struct {
	Url  types.String            `tfsdk:"url"`
	Host types.String            `tfsdk:"host"`
	Auth *gitRepositoryAuthModel `tfsdk:"auth"`
}

// gitBranchResourceModel describes the resource's data model.
type gitBranchResourceModel struct {
	Id          types.String             `tfsdk:"id"`
	Repository  gitBranchRepositoryModel `tfsdk:"repository"`
	Name        types.String             `tfsdk:"name"`
	BaseRef     types.String             `tfsdk:"base_ref"`
	BaseSha     types.String             `tfsdk:"base_sha"`
	ResolvedRef types.String             `tfsdk:"resolved_ref"`
	Patches     types.List               `tfsdk:"patches"`
}

// NewGitBranchResource creates a new instance of the git_branch resource.
func NewGitBranchResource() resource.Resource {
	return &gitBranchResource{}
}

func (r *gitBranchResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_branch"
}

func (r *gitBranchResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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
}

// resolveBranchRef resolves ref against url's remote refs, matching in
// priority order: exact name, "refs/heads/"+ref, "refs/tags/"+ref. Returns
// an error if no match is found.
func resolveBranchRef(ctx context.Context, client git.Client, url string, auth git.Auth, ref string) (string, error) {
	refs, err := client.LsRemote(ctx, url, auth)
	if err != nil {
		return "", fmt.Errorf("listing remote refs: %w", err)
	}

	candidates := []string{ref, "refs/heads/" + ref, "refs/tags/" + ref}
	for _, candidate := range candidates {
		for _, r := range refs {
			if r.Name == candidate {
				return r.Hash, nil
			}
		}
	}

	return "", fmt.Errorf("ref %q not found on %s", ref, url)
}

func (r *gitBranchResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Tracks a branch's base ref within a repository, and optionally an ordered patch stack applied on top of it. When `patches` is set, the resulting commits are force-pushed to the branch on the remote; otherwise this resource only resolves and tracks the observed base ref.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Identifier for the branch. Combines the repository URL and branch name as `<url>#<name>`.",
			},
			"repository": schema.SingleNestedAttribute{
				Required:            true,
				MarkdownDescription: "Repository the branch belongs to.",
				Attributes: map[string]schema.Attribute{
					"url": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "URL of the repository.",
						PlanModifiers: []planmodifier.String{
							stringplanmodifier.RequiresReplace(),
						},
					},
					"host": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "Type of host the repository is on. Must be one of `github`, `gitlab`, or `generic`.",
						Validators: []validator.String{
							stringvalidator.OneOf("github", "gitlab", "generic"),
						},
					},
					"auth": schema.SingleNestedAttribute{
						Optional:            true,
						MarkdownDescription: "Authentication details used to connect to the repository.",
						Attributes: map[string]schema.Attribute{
							"token": schema.StringAttribute{
								Optional:            true,
								Sensitive:           true,
								MarkdownDescription: "Token used to authenticate with the repository host.",
							},
						},
					},
				},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Name of the branch.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"base_ref": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Ref this branch is based on.",
			},
			"base_sha": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resolved commit hash of `base_ref` as of the last read.",
			},
			"resolved_ref": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resolved commit hash the branch currently tracks.",
			},
			"patches": schema.ListAttribute{
				ElementType:         types.StringType,
				Optional:            true,
				MarkdownDescription: "Ordered list of patch diffs applied on top of base_ref, in the spirit of quilt push. When set, the resulting commits are force-pushed to the branch on the remote.",
			},
		},
	}
}

// resolveModel resolves model's base_ref against its repository and updates
// base_sha/resolved_ref in place. On error it returns the error unmodified
// so callers can decide how to surface it.
//
// When model.Patches is non-empty and apply is true, the patch stack is
// applied on top of the resolved base ref and force-pushed to the branch via
// r.client.ApplyPatches, and resolved_ref is set to the resulting SHA. This
// only happens on Create/Update (apply=true); Read (apply=false) never
// mutates the remote, and instead resolves resolved_ref to the branch's
// actual current tip on the remote, reflecting real drift.
//
// When model.Patches is empty, behavior is unchanged from before patches
// existed: resolved_ref is set equal to base_sha.
func (r *gitBranchResource) resolveModel(ctx context.Context, model *gitBranchResourceModel, apply bool) error {
	auth := authFromModel(model.Repository.Host, model.Repository.Auth)
	url := model.Repository.Url.ValueString()

	hash, err := resolveBranchRef(ctx, r.client, url, auth, model.BaseRef.ValueString())
	if err != nil {
		return err
	}
	model.BaseSha = types.StringValue(hash)

	var patches []string
	if !model.Patches.IsNull() && !model.Patches.IsUnknown() {
		if diags := model.Patches.ElementsAs(ctx, &patches, false); diags.HasError() {
			return fmt.Errorf("reading patches: %v", diags)
		}
	}

	if len(patches) == 0 {
		model.ResolvedRef = types.StringValue(hash)
		return nil
	}

	if !apply {
		tip, err := resolveBranchRef(ctx, r.client, url, auth, model.Name.ValueString())
		if err != nil {
			return err
		}
		model.ResolvedRef = types.StringValue(tip)
		return nil
	}

	result, err := r.client.ApplyPatches(ctx, git.ApplyPatchesRequest{
		URL:     url,
		Auth:    auth,
		Branch:  model.Name.ValueString(),
		BaseRef: hash,
		Patches: patches,
	})
	if err != nil {
		return err
	}

	model.ResolvedRef = types.StringValue(result.ResolvedSHA)
	return nil
}

func (r *gitBranchResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var model gitBranchResourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.resolveModel(ctx, &model, true); err != nil {
		resp.Diagnostics.AddError("Unable to Resolve Base Ref", err.Error())
		return
	}

	model.Id = types.StringValue(model.Repository.Url.ValueString() + "#" + model.Name.ValueString())

	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}

func (r *gitBranchResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var model gitBranchResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.resolveModel(ctx, &model, false); err != nil {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}

func (r *gitBranchResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var model gitBranchResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.resolveModel(ctx, &model, true); err != nil {
		resp.Diagnostics.AddError("Unable to Resolve Base Ref", err.Error())
		return
	}

	model.Id = types.StringValue(model.Repository.Url.ValueString() + "#" + model.Name.ValueString())

	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}

func (r *gitBranchResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
	// No-op: this resource never deletes anything on the remote (it does not
	// own branch existence, only its base ref and patch stack). The
	// framework removes the resource from state automatically.
}

func (r *gitBranchResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	idx := strings.Index(req.ID, "#")
	if idx == -1 {
		resp.Diagnostics.AddError("Invalid Import ID", "Expected format: <url>#<name>")
		return
	}

	url := req.ID[:idx]
	name := req.ID[idx+1:]

	hash, err := resolveBranchRef(ctx, r.client, url, git.Auth{}, name)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Resolve Base Ref", err.Error())
		return
	}

	model := gitBranchResourceModel{
		Id:          types.StringValue(url + "#" + name),
		Repository:  gitBranchRepositoryModel{Url: types.StringValue(url), Host: types.StringNull(), Auth: nil},
		Name:        types.StringValue(name),
		BaseRef:     types.StringValue(name),
		BaseSha:     types.StringValue(hash),
		ResolvedRef: types.StringValue(hash),
		Patches:     types.ListNull(types.StringType),
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}
