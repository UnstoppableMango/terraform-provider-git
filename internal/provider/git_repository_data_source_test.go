package provider_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
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
	Id   types.String `tfsdk:"id"`
	Url  types.String `tfsdk:"url"`
	Host types.String `tfsdk:"host"`
	Auth *authModel   `tfsdk:"auth"`
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

		It("defines exactly the id, url, host, and auth attributes", func() {
			Expect(schemaResp.Schema.Attributes).To(HaveLen(4))
			Expect(schemaResp.Schema.Attributes).To(HaveKey("id"))
			Expect(schemaResp.Schema.Attributes).To(HaveKey("url"))
			Expect(schemaResp.Schema.Attributes).To(HaveKey("host"))
			Expect(schemaResp.Schema.Attributes).To(HaveKey("auth"))
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
			It("is required", func() {
				a := schemaResp.Schema.Attributes["url"]
				Expect(a.IsRequired()).To(BeTrue())
				Expect(a.IsOptional()).To(BeFalse())
				Expect(a.IsComputed()).To(BeFalse())
				Expect(a.IsSensitive()).To(BeFalse())
			})
		})

		Describe("host attribute", func() {
			It("is optional", func() {
				a := schemaResp.Schema.Attributes["host"]
				Expect(a.IsRequired()).To(BeFalse())
				Expect(a.IsOptional()).To(BeTrue())
				Expect(a.IsComputed()).To(BeFalse())
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
