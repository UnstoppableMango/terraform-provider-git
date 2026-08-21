package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/UnstoppableMango/terraform-provider-git/internal/git"
	"github.com/UnstoppableMango/terraform-provider-git/internal/git/execgit"
	"github.com/UnstoppableMango/terraform-provider-git/internal/git/github"
	"github.com/UnstoppableMango/terraform-provider-git/internal/git/gitlab"
	"github.com/UnstoppableMango/terraform-provider-git/internal/git/gogit"
)

// providerData is made available to resources via ConfigureRequest.ProviderData.
type providerData struct {
	GitClient    git.Client
	GithubClient github.Client
	GitlabClient gitlab.Client

	// DefaultToken is the provider-level auth.token, used by resources and
	// data sources as a fallback when their own auth.token is unset.
	DefaultToken string

	// DefaultSSH is the provider-level auth.ssh, used by resources and data
	// sources as a fallback when their own auth.ssh is unset.
	DefaultSSH *gitRepositorySSHAuthModel
}

// gitProviderModel describes the provider-level configuration data model.
type gitProviderModel struct {
	GitImplementation types.String            `tfsdk:"git_implementation"`
	Auth              *gitRepositoryAuthModel `tfsdk:"auth"`
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
		MarkdownDescription: "Declares and reconciles the state of a git repository: tracked branches and a quilt-style ordered patch stack applied on top of them.",
		Attributes: map[string]schema.Attribute{
			"git_implementation": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Git implementation backend to use. Must be one of `go-git` or `exec`. Defaults to `go-git`.",
				Validators: []validator.String{
					stringvalidator.OneOf("go-git", "exec"),
				},
			},
			"auth": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Default authentication details used to connect to repositories and hosts, applied when a resource or data source does not set its own `auth.token`/`auth.ssh`.",
				Attributes: map[string]schema.Attribute{
					"token": schema.StringAttribute{
						Optional:            true,
						Sensitive:           true,
						MarkdownDescription: "Default token used to authenticate with a repository host, unless overridden per-resource.",
					},
					"ssh": schema.SingleNestedAttribute{
						Optional:            true,
						MarkdownDescription: "Default SSH authentication details, unless overridden per-resource. Leave `private_key`/`private_key_path` unset to authenticate via a locally running SSH agent instead.",
						Attributes: map[string]schema.Attribute{
							"user": schema.StringAttribute{
								Optional:            true,
								MarkdownDescription: "SSH username. Defaults to `git`, the convention used by GitHub, GitLab, and most hosts.",
							},
							"private_key": schema.StringAttribute{
								Optional:            true,
								Sensitive:           true,
								MarkdownDescription: "PEM-encoded SSH private key content. Conflicts with `private_key_path`.",
								Validators: []validator.String{
									stringvalidator.ConflictsWith(path.MatchRelative().AtParent().AtName("private_key_path")),
								},
							},
							"private_key_path": schema.StringAttribute{
								Optional:            true,
								MarkdownDescription: "Path to a PEM-encoded SSH private key file on disk. Conflicts with `private_key`.",
							},
							"passphrase": schema.StringAttribute{
								Optional:            true,
								Sensitive:           true,
								MarkdownDescription: "Passphrase for an encrypted private key. Only honored by the go-git implementation; the exec implementation errors if set, since it cannot supply it non-interactively.",
							},
						},
					},
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

	var defaultSSH *gitRepositorySSHAuthModel
	if config.Auth != nil {
		defaultSSH = config.Auth.SSH
	}

	data := &providerData{
		GitClient:    client,
		GithubClient: github.New(),
		GitlabClient: gitlab.New(),
		DefaultToken: tokenFromModel(config.Auth, ""),
		DefaultSSH:   defaultSSH,
	}
	resp.DataSourceData = data
	resp.ResourceData = data
}

func (p *gitProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewGitRepositoryDataSource,
		NewGitPatchDataSource,
	}
}

func (p *gitProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewGitBranchResource,
	}
}
