package provider_test

import (
	"context"
	"fmt"
	"testing"

	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	pschema "github.com/hashicorp/terraform-plugin-framework/provider/schema"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/UnstoppableMango/terraform-provider-git/internal/provider"
)

var _ = Describe("GitProvider", func() {
	Describe("Metadata", func() {
		It("sets the type name to git", func() {
			p := provider.New()
			resp := &fwprovider.MetadataResponse{}

			p.Metadata(context.Background(), fwprovider.MetadataRequest{}, resp)

			Expect(resp.TypeName).To(Equal("git"))
		})
	})

	Describe("Schema", func() {
		var schemaResp fwprovider.SchemaResponse

		BeforeEach(func() {
			p := provider.New()
			schemaResp = fwprovider.SchemaResponse{}
			p.Schema(context.Background(), fwprovider.SchemaRequest{}, &schemaResp)
		})

		It("produces a schema with no errors", func() {
			Expect(schemaResp.Diagnostics.HasError()).To(BeFalse())
		})

		It("defines a git_implementation attribute", func() {
			Expect(schemaResp.Schema.Attributes).To(HaveKey("git_implementation"))
		})

		Describe("git_implementation attribute", func() {
			It("is optional, not required, not computed", func() {
				a := schemaResp.Schema.Attributes["git_implementation"]
				Expect(a.IsRequired()).To(BeFalse())
				Expect(a.IsOptional()).To(BeTrue())
				Expect(a.IsComputed()).To(BeFalse())
			})

			It("has a validator restricting it to known implementations", func() {
				a := schemaResp.Schema.Attributes["git_implementation"]

				strAttr, ok := a.(pschema.StringAttribute)
				Expect(ok).To(BeTrue(), "expected git_implementation to be a schema.StringAttribute")
				Expect(strAttr.Validators).NotTo(BeEmpty(), "expected git_implementation to have at least one validator (e.g. stringvalidator.OneOf)")
			})
		})
	})
})

// TestAccProvider_gitImplementation exercises Configure's implementation
// selection end-to-end: it's not possible to unit test Configure's wiring
// directly from this external provider_test package because the
// ConfigureResponse.ResourceData it produces is the unexported
// *provider.providerData type, so type-asserting or inspecting it from here
// isn't possible. Instead, this drives the real provider through the
// protocol and proves each git_implementation value (omitted/default,
// "go-git", and "exec") produces a working client backend by successfully
// reaching a local repository fixture via git_repository's Create, which
// calls Client.LsRemote under the hood.
func TestAccProvider_gitImplementation(t *testing.T) {
	repoPath := newTestRepo()
	repoURL := "file://" + repoPath

	tfresource.Test(t, tfresource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []tfresource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "git_repository" "default_impl" {
  url = %[1]q
}

provider "git" {
  alias              = "gogit_impl"
  git_implementation = "go-git"
}

resource "git_repository" "gogit_impl" {
  provider = git.gogit_impl
  url      = %[1]q
}

provider "git" {
  alias              = "exec_impl"
  git_implementation = "exec"
}

resource "git_repository" "exec_impl" {
  provider = git.exec_impl
  url      = %[1]q
}
`, repoURL),
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr("git_repository.default_impl", "id", repoURL),
					tfresource.TestCheckResourceAttr("git_repository.gogit_impl", "id", repoURL),
					tfresource.TestCheckResourceAttr("git_repository.exec_impl", "id", repoURL),
				),
			},
		},
	})
}
