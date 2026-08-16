package provider_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/UnstoppableMango/terraform-provider-git/internal/provider"
)

// repoModel mirrors the tfsdk tags of the unexported
// gitRepositoryResourceModel in git_repository_resource.go, so tests in this
// external provider_test package can build tfsdk.Plan/State values by hand
// without access to the unexported type.
type repoModel struct {
	Id   types.String `tfsdk:"id"`
	Url  types.String `tfsdk:"url"`
	Host types.String `tfsdk:"host"`
	Auth *authModel   `tfsdk:"auth"`
}

type authModel struct {
	Token types.String `tfsdk:"token"`
}

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

	// Without Configure ever being called, r.client stays nil (its zero
	// value). Create/Read/Update guard on r.client != nil before calling
	// LsRemote, so with no client configured they must behave exactly
	// like the old unconditional passthrough (id mirrors url, Read is a
	// no-op) instead of panicking on a nil interface call. This is the
	// key regression test for that guard.
	Describe("without a configured client", func() {
		var repoSchema rschema.Schema

		BeforeEach(func() {
			schemaResp := fwresource.SchemaResponse{}
			res.Schema(context.Background(), fwresource.SchemaRequest{}, &schemaResp)
			repoSchema = schemaResp.Schema
		})

		It("Create sets id to url without panicking or erroring", func() {
			const url = "https://example.com/repo.git"

			plan := tfsdk.Plan{Schema: repoSchema}
			Expect(plan.Set(context.Background(), &repoModel{
				Url:  types.StringValue(url),
				Host: types.StringNull(),
			}).HasError()).To(BeFalse())

			req := fwresource.CreateRequest{Plan: plan}
			resp := &fwresource.CreateResponse{State: tfsdk.State{Schema: repoSchema}}

			Expect(func() {
				res.Create(context.Background(), req, resp)
			}).NotTo(Panic())
			Expect(resp.Diagnostics.HasError()).To(BeFalse())

			var got repoModel
			Expect(resp.State.Get(context.Background(), &got).HasError()).To(BeFalse())
			Expect(got.Id.ValueString()).To(Equal(url))
			Expect(got.Url.ValueString()).To(Equal(url))
		})

		It("Read passes the state through unchanged without panicking or erroring", func() {
			const url = "https://example.com/repo.git"

			state := tfsdk.State{Schema: repoSchema}
			Expect(state.Set(context.Background(), &repoModel{
				Id:   types.StringValue(url),
				Url:  types.StringValue(url),
				Host: types.StringNull(),
			}).HasError()).To(BeFalse())

			req := fwresource.ReadRequest{State: state}
			resp := &fwresource.ReadResponse{State: tfsdk.State{Schema: repoSchema}}

			Expect(func() {
				res.Read(context.Background(), req, resp)
			}).NotTo(Panic())
			Expect(resp.Diagnostics.HasError()).To(BeFalse())

			var got repoModel
			Expect(resp.State.Get(context.Background(), &got).HasError()).To(BeFalse())
			Expect(got.Id.ValueString()).To(Equal(url))
			Expect(got.Url.ValueString()).To(Equal(url))
		})

		It("Update sets id to the new url without panicking or erroring", func() {
			const url = "https://example.com/other-repo.git"

			plan := tfsdk.Plan{Schema: repoSchema}
			Expect(plan.Set(context.Background(), &repoModel{
				Url:  types.StringValue(url),
				Host: types.StringNull(),
			}).HasError()).To(BeFalse())

			req := fwresource.UpdateRequest{Plan: plan}
			resp := &fwresource.UpdateResponse{State: tfsdk.State{Schema: repoSchema}}

			Expect(func() {
				res.Update(context.Background(), req, resp)
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
	// Create now calls LsRemote against the configured url (see the
	// r.client != nil guard in git_repository_resource.go), so the url
	// must point at something actually reachable. Point it at local git
	// repo fixtures instead of the unreachable example.com URLs the test
	// used before that behavior existed. Two distinct fixtures are used
	// so the update step (repo1 -> repo2) exercises a real url change.
	repo1URL := "file://" + newTestRepo(t)
	repo2URL := "file://" + newTestRepo(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`resource "git_repository" "test" {
  url = %[1]q
}`, repo1URL),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("git_repository.test", "url", repo1URL),
					resource.TestCheckResourceAttr("git_repository.test", "id", repo1URL),
				),
			},
			{
				Config: fmt.Sprintf(`resource "git_repository" "test" {
  url = %[1]q
}`, repo2URL),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("git_repository.test", "url", repo2URL),
					resource.TestCheckResourceAttr("git_repository.test", "id", repo2URL),
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
