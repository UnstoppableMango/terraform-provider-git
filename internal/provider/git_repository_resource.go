package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/UnstoppableMango/terraform-provider-git/internal/git"
)

// Ensure the implementation satisfies the desired interfaces.
var _ resource.Resource = &gitRepositoryResource{}
var _ resource.ResourceWithImportState = &gitRepositoryResource{}
var _ resource.ResourceWithConfigure = &gitRepositoryResource{}

// gitRepositoryResource references an existing repository. It is
// reference-only: this provider never creates or deletes repositories on
// the host. It resolves connection details (URL, host type, auth) used by
// other resources.
type gitRepositoryResource struct {
	client git.Client
}

// gitRepositoryResourceModel describes the resource data model.
type gitRepositoryResourceModel struct {
	Id   types.String            `tfsdk:"id"`
	Url  types.String            `tfsdk:"url"`
	Host types.String            `tfsdk:"host"`
	Auth *gitRepositoryAuthModel `tfsdk:"auth"`
}

// gitRepositoryAuthModel describes the auth nested attribute data model.
type gitRepositoryAuthModel struct {
	Token types.String `tfsdk:"token"`
}

// NewGitRepositoryResource creates a new instance of the git_repository
// resource.
func NewGitRepositoryResource() resource.Resource {
	return &gitRepositoryResource{}
}

func (r *gitRepositoryResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_repository"
}

func (r *gitRepositoryResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// authFromModel converts a gitRepositoryAuthModel into a git.Auth, treating
// a nil or unset token as unauthenticated.
func authFromModel(host types.String, m *gitRepositoryAuthModel) git.Auth {
	if m == nil || m.Token.IsNull() || m.Token.IsUnknown() {
		return git.Auth{}
	}

	return git.Auth{Token: m.Token.ValueString(), Host: host.ValueString()}
}

// verifyReachable checks that url is reachable with auth via the configured
// client, treating a nil client (Configure never called) as always
// reachable. Callers decide how to surface a non-nil error.
func (r *gitRepositoryResource) verifyReachable(ctx context.Context, url string, auth git.Auth) error {
	if r.client == nil {
		return nil
	}

	_, err := r.client.LsRemote(ctx, url, auth)
	return err
}

func (r *gitRepositoryResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "References an existing repository. Reference-only: this provider never creates or deletes repositories on the host. It resolves connection details (URL, host type, auth) used by other resources.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Identifier for the repository. Mirrors the `url` attribute.",
			},
			"url": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "URL of the repository.",
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
	}
}

func (r *gitRepositoryResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan gitRepositoryResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.verifyReachable(ctx, plan.Url.ValueString(), authFromModel(plan.Host, plan.Auth)); err != nil {
		resp.Diagnostics.AddError("Unable to Reach Repository", err.Error())
		return
	}

	plan.Id = plan.Url

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
}

func (r *gitRepositoryResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state gitRepositoryResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// A reachability failure here is a warning, not a hard error: unlike
	// Create/Update, Read runs on every plan/apply/destroy (as the refresh
	// step), and destroy refreshes state via Read before it can proceed. A
	// transient outage or revoked token must not block terraform destroy.
	if err := r.verifyReachable(ctx, state.Url.ValueString(), authFromModel(state.Host, state.Auth)); err != nil {
		resp.Diagnostics.AddWarning("Unable to Reach Repository", err.Error())
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
}

func (r *gitRepositoryResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan gitRepositoryResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.verifyReachable(ctx, plan.Url.ValueString(), authFromModel(plan.Host, plan.Auth)); err != nil {
		resp.Diagnostics.AddError("Unable to Reach Repository", err.Error())
		return
	}

	plan.Id = plan.Url

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
}

func (r *gitRepositoryResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
	// No external state to clean up. The framework automatically removes
	// the resource from state when this method returns without error
	// diagnostics.
}

func (r *gitRepositoryResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("url"), req.ID)...)
	if resp.Diagnostics.HasError() {
		return
	}
}
