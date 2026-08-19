package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/UnstoppableMango/terraform-provider-git/internal/git"
)

// Ensure the implementation satisfies the desired interfaces.
var _ datasource.DataSource = &gitRepositoryDataSource{}
var _ datasource.DataSourceWithConfigure = &gitRepositoryDataSource{}

// gitRepositoryDataSource references an existing repository. It is
// reference-only: this provider never creates or deletes repositories on
// the host. It resolves connection details (URL, host type, auth) used by
// other resources.
type gitRepositoryDataSource struct {
	client git.Client
}

// gitRepositoryDataSourceModel describes the data source's data model.
type gitRepositoryDataSourceModel struct {
	Id   types.String            `tfsdk:"id"`
	Url  types.String            `tfsdk:"url"`
	Host types.String            `tfsdk:"host"`
	Auth *gitRepositoryAuthModel `tfsdk:"auth"`
}

// gitRepositoryAuthModel describes the auth nested attribute data model.
type gitRepositoryAuthModel struct {
	Token types.String `tfsdk:"token"`
}

// NewGitRepositoryDataSource creates a new instance of the git_repository
// data source.
func NewGitRepositoryDataSource() datasource.DataSource {
	return &gitRepositoryDataSource{}
}

func (d *gitRepositoryDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_repository"
}

func (d *gitRepositoryDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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
}

// tokenFromModel extracts the auth token from m, treating a nil or unset
// token as unauthenticated (empty string).
func tokenFromModel(m *gitRepositoryAuthModel) string {
	if m == nil || m.Token.IsNull() || m.Token.IsUnknown() {
		return ""
	}

	return m.Token.ValueString()
}

// authFromModel converts a gitRepositoryAuthModel into a git.Auth, treating
// a nil or unset token as unauthenticated.
func authFromModel(host types.String, m *gitRepositoryAuthModel) git.Auth {
	return git.Auth{Token: tokenFromModel(m), Host: host.ValueString()}
}

// verifyReachable checks that url is reachable with auth via the configured
// client, treating a nil client (Configure never called) as always
// reachable. Callers decide how to surface a non-nil error.
func (d *gitRepositoryDataSource) verifyReachable(ctx context.Context, url string, auth git.Auth) error {
	if d.client == nil {
		return nil
	}

	_, err := d.client.LsRemote(ctx, url, auth)
	return err
}

func (d *gitRepositoryDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
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

func (d *gitRepositoryDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config gitRepositoryDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := d.verifyReachable(ctx, config.Url.ValueString(), authFromModel(config.Host, config.Auth)); err != nil {
		resp.Diagnostics.AddAttributeWarning(path.Root("url"), "Unable to Reach Repository", err.Error())
	}

	config.Id = config.Url

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
}
