package provider_test

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/UnstoppableMango/terraform-provider-git/internal/provider"
)

var _ = Describe("GitRepositoryResource", func() {
	var res fwresource.Resource

	BeforeEach(func() {
		res = provider.NewGitRepositoryResource()
	})

	Describe("Metadata", func() {
		It("derives the type name from the provider type name", func() {
			req := fwresource.MetadataRequest{ProviderTypeName: "git"}
			resp := &fwresource.MetadataResponse{}

			res.Metadata(context.Background(), req, resp)

			Expect(resp.TypeName).To(Equal("git_repository"))
		})
	})

	Describe("Schema", func() {
		var schemaResp fwresource.SchemaResponse

		BeforeEach(func() {
			schemaResp = fwresource.SchemaResponse{}
			res.Schema(context.Background(), fwresource.SchemaRequest{}, &schemaResp)
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

				hostAttr, ok := a.(rschema.StringAttribute)
				Expect(ok).To(BeTrue(), "expected host to be a schema.StringAttribute")
				Expect(hostAttr.Validators).NotTo(BeEmpty(), "expected host to have at least one validator (e.g. stringvalidator.OneOf)")
			})
		})

		Describe("auth attribute", func() {
			It("is optional and defined as a single nested object", func() {
				a := schemaResp.Schema.Attributes["auth"]
				Expect(a.IsRequired()).To(BeFalse())
				Expect(a.IsOptional()).To(BeTrue())

				_, ok := a.(rschema.NestedAttribute)
				Expect(ok).To(BeTrue(), "expected auth to be a nested attribute type")

				_, ok = a.(rschema.SingleNestedAttribute)
				Expect(ok).To(BeTrue(), "expected auth to be a schema.SingleNestedAttribute")
			})

			It("has an optional, sensitive token child attribute", func() {
				a := schemaResp.Schema.Attributes["auth"]

				nested, ok := a.(rschema.NestedAttribute)
				Expect(ok).To(BeTrue(), "expected auth to be a nested attribute type")

				tokenAttr, ok := nested.GetNestedObject().GetAttributes()["token"]
				Expect(ok).To(BeTrue(), "expected auth to define a nested token attribute")
				Expect(tokenAttr.IsRequired()).To(BeFalse())
				Expect(tokenAttr.IsOptional()).To(BeTrue())
				Expect(tokenAttr.IsSensitive()).To(BeTrue())
			})
		})
	})
})

// testAccProtoV6ProviderFactories are the provider factories used by
// acceptance tests in this package.
var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"git": providerserver.NewProtocol6WithError(provider.New()),
}

func TestAccGitRepository_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `resource "git_repository" "test" {
  url = "https://example.com/repo.git"
}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("git_repository.test", "url", "https://example.com/repo.git"),
					resource.TestCheckResourceAttr("git_repository.test", "id", "https://example.com/repo.git"),
				),
			},
			{
				Config: `resource "git_repository" "test" {
  url = "https://example.com/other-repo.git"
}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("git_repository.test", "url", "https://example.com/other-repo.git"),
					resource.TestCheckResourceAttr("git_repository.test", "id", "https://example.com/other-repo.git"),
				),
			},
			{
				ResourceName:      "git_repository.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}
