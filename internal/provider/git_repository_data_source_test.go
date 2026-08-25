package provider_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/UnstoppableMango/terraform-provider-git/internal/provider"
)

// repoModel mirrors the tfsdk tags of the unexported
// gitRepositoryDataSourceModel in git_repository_data_source.go, so tests in
// this external provider_test package can build tfsdk.Config/State values by
// hand without access to the unexported type.
type repoModel struct {
	Id    types.String `tfsdk:"id"`
	Url   types.String `tfsdk:"url"`
	Host  types.String `tfsdk:"host"`
	Auth  *authModel   `tfsdk:"auth"`
	Local *localModel  `tfsdk:"local"`
}

type localModel struct {
	Path      types.String `tfsdk:"path"`
	Remote    types.String `tfsdk:"remote"`
	RemoteUrl types.String `tfsdk:"remote_url"`
	Root      types.String `tfsdk:"root"`
	HeadRef   types.String `tfsdk:"head_ref"`
	HeadSha   types.String `tfsdk:"head_sha"`
}

type authModel struct {
	Token types.String `tfsdk:"token"`
}

var _ = Describe("GitRepositoryDataSource", func() {
	var ds datasource.DataSource

	BeforeEach(func() {
		ds = provider.NewGitRepositoryDataSource()
	})

	Describe("Metadata", func() {
		It("derives the type name from the provider type name", func() {
			req := datasource.MetadataRequest{ProviderTypeName: "git"}
			resp := &datasource.MetadataResponse{}

			ds.Metadata(context.Background(), req, resp)

			Expect(resp.TypeName).To(Equal("git_repository"))
		})
	})

	Describe("Schema", func() {
		var schemaResp datasource.SchemaResponse

		BeforeEach(func() {
			schemaResp = datasource.SchemaResponse{}
			ds.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResp)
		})

		It("produces a schema with no errors", func() {
			Expect(schemaResp.Diagnostics.HasError()).To(BeFalse())
		})

		It("defines exactly the id, url, host, auth, and local attributes", func() {
			Expect(schemaResp.Schema.Attributes).To(HaveLen(5))
			Expect(schemaResp.Schema.Attributes).To(HaveKey("id"))
			Expect(schemaResp.Schema.Attributes).To(HaveKey("url"))
			Expect(schemaResp.Schema.Attributes).To(HaveKey("host"))
			Expect(schemaResp.Schema.Attributes).To(HaveKey("auth"))
			Expect(schemaResp.Schema.Attributes).To(HaveKey("local"))
		})

		Describe("id attribute", func() {
			It("is computed only", func() {
				a := schemaResp.Schema.Attributes["id"]
				Expect(a.IsRequired()).To(BeFalse())
				Expect(a.IsOptional()).To(BeFalse())
				Expect(a.IsComputed()).To(BeTrue())
				Expect(a.IsSensitive()).To(BeFalse())
			})
		})

		Describe("url attribute", func() {
			// Optional rather than required since `local` can discover it,
			// and computed so discovery has somewhere to put the result.
			It("is optional and computed", func() {
				a := schemaResp.Schema.Attributes["url"]
				Expect(a.IsRequired()).To(BeFalse())
				Expect(a.IsOptional()).To(BeTrue())
				Expect(a.IsComputed()).To(BeTrue())
				Expect(a.IsSensitive()).To(BeFalse())
			})
		})

		Describe("host attribute", func() {
			// Computed so it can be inferred from url's hostname when the
			// config doesn't say.
			It("is optional and computed", func() {
				a := schemaResp.Schema.Attributes["host"]
				Expect(a.IsRequired()).To(BeFalse())
				Expect(a.IsOptional()).To(BeTrue())
				Expect(a.IsComputed()).To(BeTrue())
			})

			It("has a validator restricting it to known host types", func() {
				a := schemaResp.Schema.Attributes["host"]

				hostAttr, ok := a.(dschema.StringAttribute)
				Expect(ok).To(BeTrue(), "expected host to be a schema.StringAttribute")
				Expect(hostAttr.Validators).NotTo(BeEmpty(), "expected host to have at least one validator (e.g. stringvalidator.OneOf)")
			})
		})

		Describe("auth attribute", func() {
			It("is optional and defined as a single nested object", func() {
				a := schemaResp.Schema.Attributes["auth"]
				Expect(a.IsRequired()).To(BeFalse())
				Expect(a.IsOptional()).To(BeTrue())

				_, ok := a.(dschema.NestedAttribute)
				Expect(ok).To(BeTrue(), "expected auth to be a nested attribute type")

				_, ok = a.(dschema.SingleNestedAttribute)
				Expect(ok).To(BeTrue(), "expected auth to be a schema.SingleNestedAttribute")
			})

			It("has an optional, sensitive token child attribute", func() {
				a := schemaResp.Schema.Attributes["auth"]

				nested, ok := a.(dschema.NestedAttribute)
				Expect(ok).To(BeTrue(), "expected auth to be a nested attribute type")

				tokenAttr, ok := nested.GetNestedObject().GetAttributes()["token"]
				Expect(ok).To(BeTrue(), "expected auth to define a nested token attribute")
				Expect(tokenAttr.IsRequired()).To(BeFalse())
				Expect(tokenAttr.IsOptional()).To(BeTrue())
				Expect(tokenAttr.IsSensitive()).To(BeTrue())
			})
		})
	})

	Describe("local attribute", func() {
		var repoSchema dschema.Schema

		BeforeEach(func() {
			schemaResp := datasource.SchemaResponse{}
			ds.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResp)
			repoSchema = schemaResp.Schema
		})

		It("is an optional single nested attribute", func() {
			a := repoSchema.Attributes["local"]
			Expect(a.IsRequired()).To(BeFalse())
			Expect(a.IsOptional()).To(BeTrue())

			_, ok := a.(dschema.SingleNestedAttribute)
			Expect(ok).To(BeTrue(), "expected local to be a schema.SingleNestedAttribute")
		})

		It("takes path and remote as input and reports the rest as computed", func() {
			nested, ok := repoSchema.Attributes["local"].(dschema.NestedAttribute)
			Expect(ok).To(BeTrue(), "expected local to be a nested attribute type")
			attrs := nested.GetNestedObject().GetAttributes()

			Expect(attrs).To(HaveLen(6))
			for _, name := range []string{"path", "remote"} {
				Expect(attrs[name].IsOptional()).To(BeTrue(), name)
				Expect(attrs[name].IsComputed()).To(BeFalse(), name)
			}
			for _, name := range []string{"remote_url", "root", "head_ref", "head_sha"} {
				Expect(attrs[name].IsOptional()).To(BeFalse(), name)
				Expect(attrs[name].IsComputed()).To(BeTrue(), name)
			}
		})
	})

	Describe("ConfigValidators", func() {
		It("declares a validator so url and local are mutually exclusive", func() {
			validators, ok := ds.(datasource.DataSourceWithConfigValidators)
			Expect(ok).To(BeTrue(), "expected gitRepositoryDataSource to implement DataSourceWithConfigValidators")
			Expect(validators.ConfigValidators(context.Background())).To(HaveLen(1))
		})
	})

	// Discovery reads the filesystem only, so these specs run against a real
	// temporary checkout with no client configured: Read's LsRemote guard
	// keeps them off the network.
	Describe("local discovery", func() {
		var repoSchema dschema.Schema

		BeforeEach(func() {
			schemaResp := datasource.SchemaResponse{}
			ds.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResp)
			repoSchema = schemaResp.Schema
		})

		readLocal := func(model repoModel) (repoModel, diag.Diagnostics) {
			GinkgoHelper()

			built := tfsdk.State{Schema: repoSchema}
			Expect(built.Set(context.Background(), &model).HasError()).To(BeFalse())

			req := datasource.ReadRequest{Config: tfsdk.Config{Schema: repoSchema, Raw: built.Raw}}
			resp := &datasource.ReadResponse{State: tfsdk.State{Schema: repoSchema}}
			ds.Read(context.Background(), req, resp)

			var got repoModel
			if !resp.Diagnostics.HasError() {
				Expect(resp.State.Get(context.Background(), &got).HasError()).To(BeFalse())
			}

			return got, resp.Diagnostics
		}

		It("resolves url, host, and the observed head from the checkout", func() {
			dir := newGinkgoRepo("https://gitlab.com/group/repo.git")

			got, diags := readLocal(repoModel{Local: &localModel{Path: types.StringValue(dir)}})

			Expect(diags.HasError()).To(BeFalse())
			Expect(got.Url.ValueString()).To(Equal("https://gitlab.com/group/repo.git"))
			Expect(got.Id.ValueString()).To(Equal(got.Url.ValueString()))
			Expect(got.Host.ValueString()).To(Equal("gitlab"))
			Expect(got.Local.RemoteUrl.ValueString()).To(Equal("https://gitlab.com/group/repo.git"))
			Expect(got.Local.HeadRef.ValueString()).NotTo(BeEmpty())
			Expect(got.Local.HeadSha.ValueString()).To(HaveLen(40))
			Expect(got.Local.Root.ValueString()).NotTo(BeEmpty())
		})

		It("keeps an explicitly configured host", func() {
			dir := newGinkgoRepo("https://gitlab.com/group/repo.git")

			got, diags := readLocal(repoModel{
				Host:  types.StringValue("generic"),
				Local: &localModel{Path: types.StringValue(dir)},
			})

			Expect(diags.HasError()).To(BeFalse())
			Expect(got.Host.ValueString()).To(Equal("generic"))
		})

		It("rewrites an ssh remote to https when a token is available", func() {
			dir := newGinkgoRepo("git@github.com:owner/repo.git")

			got, diags := readLocal(repoModel{
				Auth:  &authModel{Token: types.StringValue("token")},
				Local: &localModel{Path: types.StringValue(dir)},
			})

			Expect(diags.HasError()).To(BeFalse())
			Expect(got.Url.ValueString()).To(Equal("https://github.com/owner/repo.git"))
			Expect(got.Local.RemoteUrl.ValueString()).To(Equal("git@github.com:owner/repo.git"))
			Expect(got.Host.ValueString()).To(Equal("github"))
		})

		It("leaves an ssh remote alone when there is no token", func() {
			dir := newGinkgoRepo("git@github.com:owner/repo.git")

			got, diags := readLocal(repoModel{Local: &localModel{Path: types.StringValue(dir)}})

			Expect(diags.HasError()).To(BeFalse())
			Expect(got.Url.ValueString()).To(Equal("git@github.com:owner/repo.git"))
		})

		It("reads a remote other than origin", func() {
			dir := newGinkgoRepo("https://github.com/owner/repo.git")
			runGit(dir, "remote", "add", "upstream", "https://github.com/upstream/repo.git")

			got, diags := readLocal(repoModel{Local: &localModel{
				Path:   types.StringValue(dir),
				Remote: types.StringValue("upstream"),
			}})

			Expect(diags.HasError()).To(BeFalse())
			Expect(got.Url.ValueString()).To(Equal("https://github.com/upstream/repo.git"))
		})

		It("reports a missing repository against local.path", func() {
			_, diags := readLocal(repoModel{Local: &localModel{
				Path: types.StringValue(GinkgoT().TempDir()),
			}})

			Expect(diags.HasError()).To(BeTrue())
			Expect(diags.Errors()[0].Summary()).To(Equal("Unable to Discover Repository"))
			Expect(attributeErrorPaths(diags)).To(ContainElement("local.path"))
		})

		It("reports a missing remote against local.remote", func() {
			dir := newGinkgoRepo("https://github.com/owner/repo.git")

			_, diags := readLocal(repoModel{Local: &localModel{
				Path:   types.StringValue(dir),
				Remote: types.StringValue("upstream"),
			}})

			Expect(diags.HasError()).To(BeTrue())
			Expect(diags.Errors()[0].Detail()).To(ContainSubstring("origin"))
			Expect(attributeErrorPaths(diags)).To(ContainElement("local.remote"))
		})
	})

	// Without Configure ever being called, d.client stays nil (its zero
	// value). Read guards on d.client != nil before calling LsRemote, so
	// with no client configured it must behave like an unconditional
	// passthrough (id mirrors url) instead of panicking on a nil interface
	// call. This is the key regression test for that guard.
	Describe("without a configured client", func() {
		var repoSchema dschema.Schema

		BeforeEach(func() {
			schemaResp := datasource.SchemaResponse{}
			ds.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResp)
			repoSchema = schemaResp.Schema
		})

		It("Read sets id to url without panicking or erroring", func() {
			const url = "https://example.com/repo.git"

			// tfsdk.Config has no Set method (unlike Plan/State), so build
			// the raw value via a throwaway State and reuse it.
			built := tfsdk.State{Schema: repoSchema}
			Expect(built.Set(context.Background(), &repoModel{
				Url:  types.StringValue(url),
				Host: types.StringNull(),
			}).HasError()).To(BeFalse())
			config := tfsdk.Config{Schema: repoSchema, Raw: built.Raw}

			req := datasource.ReadRequest{Config: config}
			resp := &datasource.ReadResponse{State: tfsdk.State{Schema: repoSchema}}

			Expect(func() {
				ds.Read(context.Background(), req, resp)
			}).NotTo(Panic())
			Expect(resp.Diagnostics.HasError()).To(BeFalse())

			var got repoModel
			Expect(resp.State.Get(context.Background(), &got).HasError()).To(BeFalse())
			Expect(got.Id.ValueString()).To(Equal(url))
			Expect(got.Url.ValueString()).To(Equal(url))
		})
	})
})

// attributeErrorPaths returns the attribute paths of every error diagnostic in
// diags that carries one, so specs can assert a diagnostic was attached to the
// attribute the user has to fix.
func attributeErrorPaths(diags diag.Diagnostics) []string {
	paths := []string{}
	for _, d := range diags.Errors() {
		withPath, ok := d.(diag.DiagnosticWithPath)
		if !ok {
			continue
		}
		paths = append(paths, withPath.Path().String())
	}

	return paths
}

// testAccProtoV6ProviderFactories are the provider factories used by
// acceptance tests in this package.
var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"git": providerserver.NewProtocol6WithError(provider.New()),
}

func TestAccGitRepository_basic(t *testing.T) {
	// Read now calls LsRemote against the configured url (see the
	// d.client != nil guard in git_repository_data_source.go), so the url
	// must point at something actually reachable. Point it at local git
	// repo fixtures instead of the unreachable example.com URLs the test
	// used before that behavior existed. Two distinct fixtures are used so
	// the second step exercises a fresh lookup (repo1 -> repo2).
	repo1URL := "file://" + newTestRepo(t)
	repo2URL := "file://" + newTestRepo(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`data "git_repository" "test" {
  url = %[1]q
}`, repo1URL),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.git_repository.test", "url", repo1URL),
					resource.TestCheckResourceAttr("data.git_repository.test", "id", repo1URL),
				),
			},
			{
				Config: fmt.Sprintf(`data "git_repository" "test" {
  url = %[1]q
}`, repo2URL),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.git_repository.test", "url", repo2URL),
					resource.TestCheckResourceAttr("data.git_repository.test", "id", repo2URL),
				),
			},
		},
	})
}
