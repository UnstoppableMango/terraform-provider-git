package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

type gitProvider struct{}

var _ provider.Provider = (*gitProvider)(nil)

func New() provider.Provider {
	return &gitProvider{}
}

func (p *gitProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "git"
}

func (p *gitProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{}
}

func (p *gitProvider) Configure(_ context.Context, _ provider.ConfigureRequest, _ *provider.ConfigureResponse) {
}

func (p *gitProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return nil
}

func (p *gitProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewGitRepositoryResource,
	}
}
