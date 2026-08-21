package provider

// This file covers provider-level auth inheritance: a provider-level
// auth.token used as a fallback when a resource/data source's own auth.token
// is unset (see DESIGN.md's "Auth" section). tokenFromModel/authFromModel
// and the client/model fields they're threaded through
// (gitRepositoryDataSource.defaultToken, gitBranchResource.defaultToken) are
// all unexported, so this lives in package provider (not provider_test) to
// reach them directly.

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/UnstoppableMango/terraform-provider-git/internal/git"
)

var _ = Describe("tokenFromModel", func() {
	It("returns the model's token when set", func() {
		m := &gitRepositoryAuthModel{Token: types.StringValue("resource-tok")}
		Expect(tokenFromModel(m, "default-tok")).To(Equal("resource-tok"))
	})

	It("falls back to defaultToken when the model is nil", func() {
		Expect(tokenFromModel(nil, "default-tok")).To(Equal("default-tok"))
	})

	It("falls back to defaultToken when the model's token is null", func() {
		m := &gitRepositoryAuthModel{Token: types.StringNull()}
		Expect(tokenFromModel(m, "default-tok")).To(Equal("default-tok"))
	})

	It("returns empty when neither the model nor defaultToken has a token", func() {
		Expect(tokenFromModel(nil, "")).To(Equal(""))
	})
})

var _ = Describe("authFromModel", func() {
	It("combines the model's own token with host", func() {
		m := &gitRepositoryAuthModel{Token: types.StringValue("resource-tok")}
		auth := authFromModel(types.StringValue("github"), m, "default-tok", nil)
		Expect(auth).To(Equal(git.Auth{Token: "resource-tok", Host: "github"}))
	})

	It("falls back to defaultToken while still setting host", func() {
		auth := authFromModel(types.StringValue("gitlab"), nil, "default-tok", nil)
		Expect(auth).To(Equal(git.Auth{Token: "default-tok", Host: "gitlab"}))
	})

	It("uses the model's own ssh block over the provider default", func() {
		m := &gitRepositoryAuthModel{SSH: &gitRepositorySSHAuthModel{
			PrivateKeyPath: types.StringValue("/resource/key"),
		}}
		defaultSSH := &gitRepositorySSHAuthModel{PrivateKeyPath: types.StringValue("/default/key")}

		auth := authFromModel(types.StringValue("github"), m, "", defaultSSH)
		Expect(auth).To(Equal(git.Auth{Host: "github", SSHPrivateKeyPath: "/resource/key"}))
	})

	It("falls back to the provider default ssh block when the model has none", func() {
		defaultSSH := &gitRepositorySSHAuthModel{PrivateKeyPath: types.StringValue("/default/key")}

		auth := authFromModel(types.StringValue("github"), nil, "", defaultSSH)
		Expect(auth).To(Equal(git.Auth{Host: "github", SSHPrivateKeyPath: "/default/key"}))
	})

	It("resolves an ssh block with a private key to SSHAgent: false", func() {
		m := &gitRepositoryAuthModel{SSH: &gitRepositorySSHAuthModel{
			PrivateKey: types.StringValue("pem-content"),
		}}

		auth := authFromModel(types.StringValue("github"), m, "", nil)
		Expect(auth.SSHAgent).To(BeFalse())
		Expect(auth.SSHPrivateKey).To(Equal("pem-content"))
	})

	It("resolves an empty ssh block (no key material) to SSHAgent: true", func() {
		m := &gitRepositoryAuthModel{SSH: &gitRepositorySSHAuthModel{}}

		auth := authFromModel(types.StringValue("github"), m, "", nil)
		Expect(auth.SSHAgent).To(BeTrue())
	})

	It("leaves all ssh fields empty when neither the model nor the default has an ssh block", func() {
		auth := authFromModel(types.StringValue("github"), nil, "", nil)
		Expect(auth.SSHAgent).To(BeFalse())
		Expect(auth.SSHPrivateKey).To(BeEmpty())
		Expect(auth.SSHPrivateKeyPath).To(BeEmpty())
	})
})

var _ = Describe("gitProvider Configure", func() {
	configure := func(model gitProviderModel) *fwprovider.ConfigureResponse {
		p := &gitProvider{}

		schemaResp := fwprovider.SchemaResponse{}
		p.Schema(context.Background(), fwprovider.SchemaRequest{}, &schemaResp)

		built := tfsdk.State{Schema: schemaResp.Schema}
		Expect(built.Set(context.Background(), &model).HasError()).To(BeFalse())

		req := fwprovider.ConfigureRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: built.Raw}}
		resp := &fwprovider.ConfigureResponse{}
		p.Configure(context.Background(), req, resp)

		return resp
	}

	It("stores the provider-level auth.token as providerData.DefaultToken", func() {
		resp := configure(gitProviderModel{
			GitImplementation: types.StringNull(),
			Auth:              &gitRepositoryAuthModel{Token: types.StringValue("provider-tok")},
		})

		Expect(resp.Diagnostics.HasError()).To(BeFalse())

		data, ok := resp.ResourceData.(*providerData)
		Expect(ok).To(BeTrue(), "expected ResourceData to be *providerData")
		Expect(data.DefaultToken).To(Equal("provider-tok"))
		Expect(resp.DataSourceData).To(Equal(resp.ResourceData))
	})

	It("leaves DefaultToken empty when auth is unset", func() {
		resp := configure(gitProviderModel{GitImplementation: types.StringNull(), Auth: nil})

		Expect(resp.Diagnostics.HasError()).To(BeFalse())

		data, ok := resp.ResourceData.(*providerData)
		Expect(ok).To(BeTrue(), "expected ResourceData to be *providerData")
		Expect(data.DefaultToken).To(Equal(""))
	})

	It("stores the provider-level auth.ssh as providerData.DefaultSSH", func() {
		resp := configure(gitProviderModel{
			GitImplementation: types.StringNull(),
			Auth: &gitRepositoryAuthModel{SSH: &gitRepositorySSHAuthModel{
				PrivateKeyPath: types.StringValue("/provider/key"),
			}},
		})

		Expect(resp.Diagnostics.HasError()).To(BeFalse())

		data, ok := resp.ResourceData.(*providerData)
		Expect(ok).To(BeTrue(), "expected ResourceData to be *providerData")
		Expect(data.DefaultSSH).NotTo(BeNil())
		Expect(data.DefaultSSH.PrivateKeyPath.ValueString()).To(Equal("/provider/key"))
	})

	It("leaves DefaultSSH nil when auth is unset", func() {
		resp := configure(gitProviderModel{GitImplementation: types.StringNull(), Auth: nil})

		Expect(resp.Diagnostics.HasError()).To(BeFalse())

		data, ok := resp.ResourceData.(*providerData)
		Expect(ok).To(BeTrue(), "expected ResourceData to be *providerData")
		Expect(data.DefaultSSH).To(BeNil())
	})
})

// authCapturingGitClient is a minimal git.Client double that records the
// auth it was called with. Unlike the fakeGitClient in
// git_branch_resource_test.go (package provider_test, unreachable from
// here), this one always succeeds, resolving any ref name to a fixed hash
// under refs/heads/, since these tests only care about which auth reached
// the client.
type authCapturingGitClient struct {
	gotAuth git.Auth
}

var _ git.Client = (*authCapturingGitClient)(nil)

func (f *authCapturingGitClient) LsRemote(_ context.Context, _ string, auth git.Auth) ([]git.Ref, error) {
	f.gotAuth = auth
	return []git.Ref{{Name: "refs/heads/main", Hash: "deadbeef"}}, nil
}

func (f *authCapturingGitClient) ApplyPatches(_ context.Context, req git.ApplyPatchesRequest) (git.ApplyPatchesResult, error) {
	f.gotAuth = req.Auth
	return git.ApplyPatchesResult{ResolvedSHA: req.BaseRef}, nil
}

func (f *authCapturingGitClient) IsAncestor(_ context.Context, _ string, auth git.Auth, _, _ string) (bool, error) {
	f.gotAuth = auth
	return true, nil
}

var _ = Describe("gitRepositoryDataSource default token fallback", func() {
	var repoDSSchema datasource.SchemaResponse

	BeforeEach(func() {
		repoDSSchema = datasource.SchemaResponse{}
		(&gitRepositoryDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &repoDSSchema)
	})

	newRepoReadRequest := func(model gitRepositoryDataSourceModel) (datasource.ReadRequest, *datasource.ReadResponse) {
		ctx := context.Background()
		built := tfsdk.State{Schema: repoDSSchema.Schema}
		Expect(built.Set(ctx, &model).HasError()).To(BeFalse())
		return datasource.ReadRequest{Config: tfsdk.Config{Schema: repoDSSchema.Schema, Raw: built.Raw}},
			&datasource.ReadResponse{State: tfsdk.State{Schema: repoDSSchema.Schema}}
	}

	It("falls back to the provider default token when repository auth is unset", func() {
		fake := &authCapturingGitClient{}
		ds := &gitRepositoryDataSource{client: fake, defaultToken: "default-tok"}

		req, resp := newRepoReadRequest(gitRepositoryDataSourceModel{
			Id:   types.StringUnknown(),
			Url:  types.StringValue("https://example.com/repo.git"),
			Host: types.StringValue("github"),
		})

		ds.Read(context.Background(), req, resp)

		Expect(resp.Diagnostics.HasError()).To(BeFalse())
		Expect(fake.gotAuth).To(Equal(git.Auth{Token: "default-tok", Host: "github"}))
	})

	It("prefers the repository's own token over the provider default", func() {
		fake := &authCapturingGitClient{}
		ds := &gitRepositoryDataSource{client: fake, defaultToken: "default-tok"}

		req, resp := newRepoReadRequest(gitRepositoryDataSourceModel{
			Id:   types.StringUnknown(),
			Url:  types.StringValue("https://example.com/repo.git"),
			Host: types.StringValue("github"),
			Auth: &gitRepositoryAuthModel{Token: types.StringValue("resource-tok")},
		})

		ds.Read(context.Background(), req, resp)

		Expect(resp.Diagnostics.HasError()).To(BeFalse())
		Expect(fake.gotAuth).To(Equal(git.Auth{Token: "resource-tok", Host: "github"}))
	})
})

var _ = Describe("gitBranchResource default token fallback", func() {
	baseModel := func(auth *gitRepositoryAuthModel) gitBranchResourceModel {
		return gitBranchResourceModel{
			Repository: gitBranchRepositoryModel{
				Url:  types.StringValue("https://example.com/repo.git"),
				Host: types.StringValue("gitlab"),
				Auth: auth,
			},
			Name:       types.StringValue("main"),
			BaseRef:    types.StringValue("main"),
			Patches:    types.ListNull(types.StringType),
			OnConflict: types.StringValue("force"),
		}
	}

	It("falls back to the provider default token when repository auth is unset", func() {
		fake := &authCapturingGitClient{}
		r := &gitBranchResource{client: fake, defaultToken: "default-tok"}

		model := baseModel(nil)
		Expect(r.resolveModel(context.Background(), &model, false, "")).To(Succeed())
		Expect(fake.gotAuth).To(Equal(git.Auth{Token: "default-tok", Host: "gitlab"}))
	})

	It("prefers the repository's own token over the provider default", func() {
		fake := &authCapturingGitClient{}
		r := &gitBranchResource{client: fake, defaultToken: "default-tok"}

		model := baseModel(&gitRepositoryAuthModel{Token: types.StringValue("resource-tok")})
		Expect(r.resolveModel(context.Background(), &model, false, "")).To(Succeed())
		Expect(fake.gotAuth).To(Equal(git.Auth{Token: "resource-tok", Host: "gitlab"}))
	})
})
