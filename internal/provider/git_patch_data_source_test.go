package provider_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/UnstoppableMango/terraform-provider-git/internal/provider"
)

// patchModel mirrors the tfsdk tags of the unexported gitPatchResourceModel
// in git_patch_data_source.go, so tests in this external provider_test
// package can build tfsdk.Config/State values by hand without access to the
// unexported type. See repoModel/authModel in
// git_repository_data_source_test.go for the established pattern this
// follows.
type patchModel struct {
	Id      types.String      `tfsdk:"id"`
	Content types.String      `tfsdk:"content"`
	File    types.String      `tfsdk:"file"`
	Diff    types.String      `tfsdk:"diff"`
	Github  *patchGithubModel `tfsdk:"github"`
	Gitlab  *patchGitlabModel `tfsdk:"gitlab"`
	Auth    *authModel        `tfsdk:"auth"`
}

type patchGithubModel struct {
	Repository types.String `tfsdk:"repository"`
	Pr         types.Int64  `tfsdk:"pr"`
	Commit     types.String `tfsdk:"commit"`
	Sha        types.String `tfsdk:"sha"`
}

type patchGitlabModel struct {
	Project types.String `tfsdk:"project"`
	Mr      types.Int64  `tfsdk:"mr"`
	Commit  types.String `tfsdk:"commit"`
	Sha     types.String `tfsdk:"sha"`
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// newPatchReadRequest builds a ReadRequest/ReadResponse pair from a
// hand-built patchModel. tfsdk.Config has no Set method (unlike Plan/State),
// so the raw value is built via a throwaway State and reused, mirroring the
// pattern in git_repository_data_source_test.go.
func newPatchReadRequest(patchSchema dschema.Schema, model patchModel) (datasource.ReadRequest, *datasource.ReadResponse) {
	ctx := context.Background()

	built := tfsdk.State{Schema: patchSchema}
	Expect(built.Set(ctx, &model).HasError()).To(BeFalse())

	req := datasource.ReadRequest{
		Config: tfsdk.Config{Schema: patchSchema, Raw: built.Raw},
	}
	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: patchSchema}}

	return req, resp
}

var _ = Describe("GitPatchDataSource", func() {
	var ds datasource.DataSource
	var patchSchema dschema.Schema

	BeforeEach(func() {
		ds = provider.NewGitPatchDataSource()

		schemaResp := datasource.SchemaResponse{}
		ds.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResp)
		patchSchema = schemaResp.Schema
	})

	Describe("Metadata", func() {
		It("derives the type name from the provider type name", func() {
			req := datasource.MetadataRequest{ProviderTypeName: "git"}
			resp := &datasource.MetadataResponse{}

			ds.Metadata(context.Background(), req, resp)

			Expect(resp.TypeName).To(Equal("git_patch"))
		})
	})

	Describe("Schema", func() {
		It("produces a schema with no errors", func() {
			schemaResp := datasource.SchemaResponse{}
			ds.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResp)

			Expect(schemaResp.Diagnostics.HasError()).To(BeFalse())
		})

		It("defines exactly the id, content, file, diff, github, gitlab, and auth attributes", func() {
			Expect(patchSchema.Attributes).To(HaveLen(7))
			Expect(patchSchema.Attributes).To(HaveKey("id"))
			Expect(patchSchema.Attributes).To(HaveKey("content"))
			Expect(patchSchema.Attributes).To(HaveKey("file"))
			Expect(patchSchema.Attributes).To(HaveKey("diff"))
			Expect(patchSchema.Attributes).To(HaveKey("github"))
			Expect(patchSchema.Attributes).To(HaveKey("gitlab"))
			Expect(patchSchema.Attributes).To(HaveKey("auth"))
		})

		Describe("id attribute", func() {
			It("is computed only", func() {
				a := patchSchema.Attributes["id"]
				Expect(a.IsRequired()).To(BeFalse())
				Expect(a.IsOptional()).To(BeFalse())
				Expect(a.IsComputed()).To(BeTrue())
			})
		})

		Describe("content attribute", func() {
			It("is optional, not computed", func() {
				a := patchSchema.Attributes["content"]
				Expect(a.IsRequired()).To(BeFalse())
				Expect(a.IsOptional()).To(BeTrue())
				Expect(a.IsComputed()).To(BeFalse())
			})
		})

		Describe("file attribute", func() {
			It("is optional, not computed", func() {
				a := patchSchema.Attributes["file"]
				Expect(a.IsRequired()).To(BeFalse())
				Expect(a.IsOptional()).To(BeTrue())
				Expect(a.IsComputed()).To(BeFalse())
			})
		})

		Describe("diff attribute", func() {
			It("is computed only", func() {
				a := patchSchema.Attributes["diff"]
				Expect(a.IsRequired()).To(BeFalse())
				Expect(a.IsOptional()).To(BeFalse())
				Expect(a.IsComputed()).To(BeTrue())
			})
		})

		Describe("github attribute", func() {
			It("is optional and defined as a single nested object", func() {
				a := patchSchema.Attributes["github"]
				Expect(a.IsRequired()).To(BeFalse())
				Expect(a.IsOptional()).To(BeTrue())

				_, ok := a.(dschema.NestedAttribute)
				Expect(ok).To(BeTrue(), "expected github to be a nested attribute type")

				_, ok = a.(dschema.SingleNestedAttribute)
				Expect(ok).To(BeTrue(), "expected github to be a schema.SingleNestedAttribute")
			})

			It("defines repository (required), pr (optional), commit (optional), and sha (computed)", func() {
				nested, ok := patchSchema.Attributes["github"].(dschema.NestedAttribute)
				Expect(ok).To(BeTrue(), "expected github to be a nested attribute type")
				attrs := nested.GetNestedObject().GetAttributes()

				repo, ok := attrs["repository"]
				Expect(ok).To(BeTrue())
				Expect(repo.IsRequired()).To(BeTrue())

				pr, ok := attrs["pr"]
				Expect(ok).To(BeTrue())
				Expect(pr.IsOptional()).To(BeTrue())
				Expect(pr.IsRequired()).To(BeFalse())

				commit, ok := attrs["commit"]
				Expect(ok).To(BeTrue())
				Expect(commit.IsOptional()).To(BeTrue())
				Expect(commit.IsRequired()).To(BeFalse())

				sha, ok := attrs["sha"]
				Expect(ok).To(BeTrue())
				Expect(sha.IsComputed()).To(BeTrue())
				Expect(sha.IsOptional()).To(BeFalse())
				Expect(sha.IsRequired()).To(BeFalse())
			})
		})

		Describe("gitlab attribute", func() {
			It("is optional and defined as a single nested object", func() {
				a := patchSchema.Attributes["gitlab"]
				Expect(a.IsRequired()).To(BeFalse())
				Expect(a.IsOptional()).To(BeTrue())

				_, ok := a.(dschema.NestedAttribute)
				Expect(ok).To(BeTrue(), "expected gitlab to be a nested attribute type")

				_, ok = a.(dschema.SingleNestedAttribute)
				Expect(ok).To(BeTrue(), "expected gitlab to be a schema.SingleNestedAttribute")
			})

			It("defines project (required), mr (optional), commit (optional), and sha (computed)", func() {
				nested, ok := patchSchema.Attributes["gitlab"].(dschema.NestedAttribute)
				Expect(ok).To(BeTrue(), "expected gitlab to be a nested attribute type")
				attrs := nested.GetNestedObject().GetAttributes()

				project, ok := attrs["project"]
				Expect(ok).To(BeTrue())
				Expect(project.IsRequired()).To(BeTrue())

				mr, ok := attrs["mr"]
				Expect(ok).To(BeTrue())
				Expect(mr.IsOptional()).To(BeTrue())
				Expect(mr.IsRequired()).To(BeFalse())

				commit, ok := attrs["commit"]
				Expect(ok).To(BeTrue())
				Expect(commit.IsOptional()).To(BeTrue())
				Expect(commit.IsRequired()).To(BeFalse())

				sha, ok := attrs["sha"]
				Expect(ok).To(BeTrue())
				Expect(sha.IsComputed()).To(BeTrue())
				Expect(sha.IsOptional()).To(BeFalse())
				Expect(sha.IsRequired()).To(BeFalse())
			})
		})

		Describe("auth attribute", func() {
			It("has an optional, sensitive token child attribute", func() {
				nested, ok := patchSchema.Attributes["auth"].(dschema.NestedAttribute)
				Expect(ok).To(BeTrue(), "expected auth to be a nested attribute type")

				tokenAttr, ok := nested.GetNestedObject().GetAttributes()["token"]
				Expect(ok).To(BeTrue())
				Expect(tokenAttr.IsOptional()).To(BeTrue())
				Expect(tokenAttr.IsSensitive()).To(BeTrue())
			})
		})
	})

	Describe("Read", func() {
		Context("with content set", func() {
			It("resolves diff to content and id to its sha256", func() {
				const content = "diff --git a/x b/x\nindex 0000000..1111111 100644\n--- a/x\n+++ b/x\n@@ -0,0 +1 @@\n+hello\n"

				model := patchModel{
					Id:      types.StringUnknown(),
					Content: types.StringValue(content),
					File:    types.StringNull(),
					Diff:    types.StringUnknown(),
				}
				req, resp := newPatchReadRequest(patchSchema, model)

				ds.Read(context.Background(), req, resp)

				Expect(resp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", resp.Diagnostics))

				var got patchModel
				Expect(resp.State.Get(context.Background(), &got).HasError()).To(BeFalse())
				Expect(got.Diff.ValueString()).To(Equal(content))
				Expect(got.Id.ValueString()).To(Equal(sha256Hex(content)))
			})
		})

		Context("with file set", func() {
			It("resolves diff to the file contents and id to their sha256", func() {
				const content = "diff --git a/y b/y\nindex 0000000..2222222 100644\n--- a/y\n+++ b/y\n@@ -0,0 +1 @@\n+world\n"

				dir := GinkgoT().TempDir()
				path := filepath.Join(dir, "patch.diff")
				Expect(os.WriteFile(path, []byte(content), 0o644)).To(Succeed())

				model := patchModel{
					Id:      types.StringUnknown(),
					Content: types.StringNull(),
					File:    types.StringValue(path),
					Diff:    types.StringUnknown(),
				}
				req, resp := newPatchReadRequest(patchSchema, model)

				ds.Read(context.Background(), req, resp)

				Expect(resp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", resp.Diagnostics))

				var got patchModel
				Expect(resp.State.Get(context.Background(), &got).HasError()).To(BeFalse())
				Expect(got.Diff.ValueString()).To(Equal(content))
				Expect(got.Id.ValueString()).To(Equal(sha256Hex(content)))
			})
		})

		Context("determinism", func() {
			It("produces the same id for the same diff content regardless of source", func() {
				const content = "diff --git a/z b/z\nindex 0000000..3333333 100644\n--- a/z\n+++ b/z\n@@ -0,0 +1 @@\n+same\n"

				dir := GinkgoT().TempDir()
				path := filepath.Join(dir, "same.diff")
				Expect(os.WriteFile(path, []byte(content), 0o644)).To(Succeed())

				contentReq, contentResp := newPatchReadRequest(patchSchema, patchModel{
					Id:      types.StringUnknown(),
					Content: types.StringValue(content),
					File:    types.StringNull(),
					Diff:    types.StringUnknown(),
				})
				ds.Read(context.Background(), contentReq, contentResp)
				Expect(contentResp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", contentResp.Diagnostics))

				fileReq, fileResp := newPatchReadRequest(patchSchema, patchModel{
					Id:      types.StringUnknown(),
					Content: types.StringNull(),
					File:    types.StringValue(path),
					Diff:    types.StringUnknown(),
				})
				ds.Read(context.Background(), fileReq, fileResp)
				Expect(fileResp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", fileResp.Diagnostics))

				var gotFromContent, gotFromFile patchModel
				Expect(contentResp.State.Get(context.Background(), &gotFromContent).HasError()).To(BeFalse())
				Expect(fileResp.State.Get(context.Background(), &gotFromFile).HasError()).To(BeFalse())

				Expect(gotFromContent.Diff.ValueString()).To(Equal(gotFromFile.Diff.ValueString()))
				Expect(gotFromContent.Id.ValueString()).To(Equal(gotFromFile.Id.ValueString()))
			})
		})
	})
})

func TestAccGitPatch_exactlyOneSource(t *testing.T) {
	tfresource.Test(t, tfresource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []tfresource.TestStep{
			{
				Config: `data "git_patch" "test" {
  content = "diff --git a/x b/x\n+hello\n"
  file    = "/tmp/does-not-need-to-exist.diff"
}`,
				ExpectError: regexp.MustCompile(`(?i)(one \(and only one\)|invalid attribute combination)`),
			},
		},
	})
}
