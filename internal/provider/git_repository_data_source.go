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
	client       git.Client
	defaultToken string
	defaultSSH   *gitRepositorySSHAuthModel
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
	Token types.String               `tfsdk:"token"`
	SSH   *gitRepositorySSHAuthModel `tfsdk:"ssh"`
}

// gitRepositorySSHAuthModel describes the auth.ssh nested attribute data
// model. An SSH block with neither PrivateKey nor PrivateKeyPath set means
// "authenticate via a locally running SSH agent".
type gitRepositorySSHAuthModel struct {
	User           types.String `tfsdk:"user"`
	PrivateKey     types.String `tfsdk:"private_key"`
	PrivateKeyPath types.String `tfsdk:"private_key_path"`
	Passphrase     types.String `tfsdk:"passphrase"`
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
	d.defaultSSH = data.DefaultSSH
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
// token of its own, and to defaultSSH (the provider-level auth.ssh, if any)
// when m has no ssh block of its own. Unlike token (which falls back
// field-by-field), ssh falls back as a whole block: an own ssh block, if set,
// wins over the provider default entirely, since merging individual fields
// between the two has no sane semantics.
func authFromModel(host types.String, m *gitRepositoryAuthModel, defaultToken string, defaultSSH *gitRepositorySSHAuthModel) git.Auth {
	auth := git.Auth{Token: tokenFromModel(m, defaultToken), Host: host.ValueString()}

	ssh := defaultSSH
	if m != nil && m.SSH != nil {
		ssh = m.SSH
	}
	if ssh != nil {
		auth.SSHUser = ssh.User.ValueString()
		auth.SSHPrivateKey = ssh.PrivateKey.ValueString()
		auth.SSHPrivateKeyPath = ssh.PrivateKeyPath.ValueString()
		auth.SSHPassphrase = ssh.Passphrase.ValueString()
		auth.SSHAgent = auth.SSHPrivateKey == "" && auth.SSHPrivateKeyPath == ""
	}

	return auth
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
					"ssh": schema.SingleNestedAttribute{
						Optional:            true,
						MarkdownDescription: "SSH authentication details, for repository URLs using the ssh:// or git@host: scheme. Leave `private_key`/`private_key_path` unset to authenticate via a locally running SSH agent instead.",
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

func (d *gitRepositoryDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config gitRepositoryDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := d.verifyReachable(ctx, config.Url.ValueString(), authFromModel(config.Host, config.Auth, d.defaultToken, d.defaultSSH)); err != nil {
		resp.Diagnostics.AddAttributeWarning(path.Root("url"), "Unable to Reach Repository", err.Error())
	}

	config.Id = config.Url

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
}
