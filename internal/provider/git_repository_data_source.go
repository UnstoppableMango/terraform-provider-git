package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/UnstoppableMango/terraform-provider-git/internal/git"
	"github.com/UnstoppableMango/terraform-provider-git/internal/git/local"
)

// Ensure the implementation satisfies the desired interfaces.
var _ datasource.DataSource = &gitRepositoryDataSource{}
var _ datasource.DataSourceWithConfigure = &gitRepositoryDataSource{}
var _ datasource.DataSourceWithConfigValidators = &gitRepositoryDataSource{}

// gitRepositoryDataSource references an existing repository. It is
// reference-only: this provider never creates or deletes repositories on
// the host. It resolves connection details (URL, host type, auth) used by
// other resources.
type gitRepositoryDataSource struct {
	client       git.Client
	defaultToken string
}

// gitRepositoryDataSourceModel describes the data source's data model.
type gitRepositoryDataSourceModel struct {
	Id    types.String             `tfsdk:"id"`
	Url   types.String             `tfsdk:"url"`
	Host  types.String             `tfsdk:"host"`
	Auth  *gitRepositoryAuthModel  `tfsdk:"auth"`
	Local *gitRepositoryLocalModel `tfsdk:"local"`
}

// gitRepositoryLocalModel describes the local nested attribute data model:
// the inputs for discovering the repository from a checkout on disk, and what
// that discovery observed.
type gitRepositoryLocalModel struct {
	Path      types.String `tfsdk:"path"`
	Remote    types.String `tfsdk:"remote"`
	RemoteUrl types.String `tfsdk:"remote_url"`
	Root      types.String `tfsdk:"root"`
	HeadRef   types.String `tfsdk:"head_ref"`
	HeadSha   types.String `tfsdk:"head_sha"`
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
	d.defaultToken = data.DefaultToken
}

// tokenFromModel extracts the auth token from m, falling back to
// defaultToken (the provider-level auth.token, if any) when m has no token
// of its own. A nil m is treated the same as an unset token.
func tokenFromModel(m *gitRepositoryAuthModel, defaultToken string) string {
	if m == nil || m.Token.IsNull() || m.Token.IsUnknown() {
		return defaultToken
	}

	return m.Token.ValueString()
}

// authFromModel converts a gitRepositoryAuthModel into a git.Auth, falling
// back to defaultToken (the provider-level auth.token, if any) when m has no
// token of its own.
func authFromModel(host types.String, m *gitRepositoryAuthModel, defaultToken string) git.Auth {
	return git.Auth{Token: tokenFromModel(m, defaultToken), Host: host.ValueString()}
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
		MarkdownDescription: "References an existing repository. Reference-only: this provider never creates or deletes repositories on the host. It resolves connection details (URL, host type, auth) used by other resources. Exactly one of `url` or `local` must be set: `url` names the repository directly, while `local` discovers it from a checkout on disk, so a configuration stored inside the repository it manages does not have to hardcode its own URL.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Identifier for the repository. Mirrors the `url` attribute.",
			},
			"url": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "URL of the repository. Computed from the discovered remote when `local` is set.",
			},
			"host": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Type of host the repository is on. Must be one of `github`, `gitlab`, or `generic`. Inferred from `url`'s hostname when unset.",
				Validators: []validator.String{
					stringvalidator.OneOf("github", "gitlab", "generic"),
				},
			},
			"auth": authSchemaAttribute("Authentication details used to connect to the repository."),
			"local": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Discovers the repository from a checkout on the filesystem, reading `url` from one of its git remotes instead of taking it literally. Set `local = {}` to discover the repository the Terraform run is executing from.",
				Attributes: map[string]schema.Attribute{
					"path": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "Path anywhere inside the working copy to discover; parent directories are walked until a repository is found. Defaults to the directory Terraform was invoked from. Relative paths resolve against the provider process's working directory, so prefer `path.root` when the configuration may be applied from elsewhere.",
					},
					"remote": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "Name of the git remote to read the URL from. Defaults to `origin`.",
					},
					"remote_url": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "URL of `remote` exactly as the discovered repository's git config records it, before any rewrite to `https`. Differs from `url` when an SSH remote was rewritten for token auth.",
					},
					"root": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Absolute path of the discovered working tree's root. Empty for a bare repository.",
					},
					"head_ref": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Name of the branch currently checked out in the discovered working copy. Empty when `HEAD` is detached or the repository has no commits.",
					},
					"head_sha": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Commit `HEAD` points at in the discovered working copy. Empty when the repository has no commits.",
					},
				},
			},
		},
	}
}

// ConfigValidators enforces that exactly one of url or local is set: the
// repository is either named directly or discovered from disk.
func (d *gitRepositoryDataSource) ConfigValidators(_ context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		datasourcevalidator.ExactlyOneOf(
			path.MatchRoot("url"),
			path.MatchRoot("local"),
		),
	}
}

// discover resolves config.Url, config.Host, and the computed attributes of
// config.Local from the repository checked out on disk. token is the auth
// token already resolved for this data source: an SSH remote URL is only
// rewritten to https when there is a token to authenticate with, since
// without one the exec backend can still reach it over SSH.
func discover(config *gitRepositoryDataSourceModel, token string) error {
	result, err := local.Discover(config.Local.Path.ValueString(), config.Local.Remote.ValueString())
	if err != nil {
		return err
	}

	url := result.RemoteURL
	if token != "" {
		url, _ = git.NormalizeURL(url)
	}

	config.Url = types.StringValue(url)
	config.Local.RemoteUrl = types.StringValue(result.RemoteURL)
	config.Local.Root = types.StringValue(result.Root)
	config.Local.HeadRef = types.StringValue(result.HeadRef)
	config.Local.HeadSha = types.StringValue(result.HeadSHA)

	return nil
}

func (d *gitRepositoryDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config gitRepositoryDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	token := tokenFromModel(config.Auth, d.defaultToken)

	if config.Local != nil {
		if err := discover(&config, token); err != nil {
			if notRepo, ok := errors.AsType[*local.NotARepositoryError](err); ok {
				resp.Diagnostics.AddAttributeError(
					path.Root("local").AtName("path"),
					"Unable to Discover Repository",
					notRepo.Error(),
				)
				return
			}
			if noRemote, ok := errors.AsType[*local.RemoteNotFoundError](err); ok {
				resp.Diagnostics.AddAttributeError(
					path.Root("local").AtName("remote"),
					"Unable to Discover Repository",
					noRemote.Error(),
				)
				return
			}
			resp.Diagnostics.AddError("Unable to Discover Repository", err.Error())
			return
		}
	}

	if config.Host.IsNull() || config.Host.IsUnknown() {
		config.Host = types.StringValue(git.HostFromURL(config.Url.ValueString()))
	}

	if err := d.verifyReachable(ctx, config.Url.ValueString(), authFromModel(config.Host, config.Auth, d.defaultToken)); err != nil {
		resp.Diagnostics.AddAttributeWarning(path.Root("url"), "Unable to Reach Repository", err.Error())
	}

	config.Id = config.Url

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
}
