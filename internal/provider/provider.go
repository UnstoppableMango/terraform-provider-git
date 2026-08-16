package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/UnstoppableMango/terraform-provider-git/internal/git"
	"github.com/UnstoppableMango/terraform-provider-git/internal/git/execgit"
	"github.com/UnstoppableMango/terraform-provider-git/internal/git/gogit"
)

// providerData is made available to resources via ConfigureRequest.ProviderData.
type providerData struct {
	GitClient git.Client
}

// gitProviderModel describes the provider-level configuration data model.
type gitProviderModel struct {
	GitImplementation types.String `tfsdk:"git_implementation"`
}

type gitProvider struct{}

var _ provider.Provider = (*gitProvider)(nil)

func New() provider.Provider {
	return &gitProvider{}
}

func (p *gitProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "git"
}

func (p *gitProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"git_implementation": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Git implementation backend to use. Must be one of `go-git` or `exec`. Defaults to `go-git`.",
				Validators: []validator.String{
					stringvalidator.OneOf("go-git", "exec"),
				},
			},
		},
	}
}

func (p *gitProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config gitProviderModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	implementation := "go-git"
	if !config.GitImplementation.IsNull() && !config.GitImplementation.IsUnknown() && config.GitImplementation.ValueString() != "" {
		implementation = config.GitImplementation.ValueString()
	}

	var client git.Client
	switch implementation {
	case "exec":
		client = execgit.New()
	default:
		client = gogit.New()
	}

	resp.ResourceData = &providerData{GitClient: client}
}

func (p *gitProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewGitRepositoryDataSource,
	}
}

func (p *gitProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{}
}
