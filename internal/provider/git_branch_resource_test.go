package provider_test

import (
	"context"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/UnstoppableMango/terraform-provider-git/internal/git"
	"github.com/UnstoppableMango/terraform-provider-git/internal/provider"
)

// branchModel and branchRepoModel mirror the tfsdk tags of the unexported
// gitBranchResourceModel/gitBranchRepositoryModel in git_branch_resource.go,
// so tests in this external provider_test package can build
// tfsdk.Config/Plan/State values by hand without access to the unexported
// types. authModel (the auth nested model) is already defined in
// git_repository_data_source_test.go and is reused here, since the
// git_branch auth attribute is shaped identically to git_repository's.
type branchModel struct {
	Id          types.String    `tfsdk:"id"`
	Repository  branchRepoModel `tfsdk:"repository"`
	Name        types.String    `tfsdk:"name"`
	BaseRef     types.String    `tfsdk:"base_ref"`
	BaseSha     types.String    `tfsdk:"base_sha"`
	ResolvedRef types.String    `tfsdk:"resolved_ref"`
}

type branchRepoModel struct {
	Url  types.String `tfsdk:"url"`
	Host types.String `tfsdk:"host"`
	Auth *authModel   `tfsdk:"auth"`
}

// fakeGitClient is a test double for git.Client, in the style of
// fakeGitHubClient in testutil_test.go: each method is backed by a
// configurable function field, and a nil field panics if called so tests
// only need to set the functions relevant to what they exercise.
type fakeGitClient struct {
	lsRemoteFunc func(ctx context.Context, url string, auth git.Auth) ([]git.Ref, error)

	gotURL  string
	gotAuth git.Auth
}

var _ git.Client = (*fakeGitClient)(nil)

func (f *fakeGitClient) LsRemote(ctx context.Context, url string, auth git.Auth) ([]git.Ref, error) {
	if f.lsRemoteFunc == nil {
		panic("fakeGitClient: LsRemote called but lsRemoteFunc is nil")
	}
	f.gotURL = url
	f.gotAuth = auth
	return f.lsRemoteFunc(ctx, url, auth)
}

// newBranchResourceWithClient returns a git_branch resource.Resource with
// its unexported client field set to client. The concrete type returned by
// provider.NewGitBranchResource, and the providerData type normally used to
// populate that field via Configure, are both unexported and therefore out
// of reach from this external test package. Reflection is used here purely
// to reach the private field on the struct we already legitimately obtained
// through the exported constructor, so tests can exercise Create/Read/
// Update/ImportState against a controlled fake client without needing
// exported test-only seams in the implementation.
func newBranchResourceWithClient(client git.Client) resource.Resource {
	r := provider.NewGitBranchResource()

	v := reflect.ValueOf(r)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	f := v.FieldByName("client")
	Expect(f.IsValid()).To(BeTrue(), "expected gitBranchResource to have a client field")

	settable := reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
	settable.Set(reflect.ValueOf(client))

	return r
}

func branchResourceSchema(r resource.Resource) rschema.Schema {
	schemaResp := resource.SchemaResponse{}
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)
	return schemaResp.Schema
}

// buildBranchConfig builds a tfsdk.Config from model. tfsdk.Config has no
// Set method (unlike Plan/State), so the raw value is built via a throwaway
// State and reused, mirroring the pattern in
// git_repository_data_source_test.go.
func buildBranchConfig(s rschema.Schema, model branchModel) tfsdk.Config {
	built := tfsdk.State{Schema: s}
	Expect(built.Set(context.Background(), &model).HasError()).To(BeFalse())
	return tfsdk.Config{Schema: s, Raw: built.Raw}
}

func buildBranchPlan(s rschema.Schema, model branchModel) tfsdk.Plan {
	plan := tfsdk.Plan{Schema: s}
	Expect(plan.Set(context.Background(), &model).HasError()).To(BeFalse())
	return plan
}

func buildBranchState(s rschema.Schema, model branchModel) tfsdk.State {
	state := tfsdk.State{Schema: s}
	Expect(state.Set(context.Background(), &model).HasError()).To(BeFalse())
	return state
}

var _ = Describe("GitBranchResource", func() {
	var r resource.Resource
	var branchSchema rschema.Schema

	BeforeEach(func() {
		r = provider.NewGitBranchResource()
		branchSchema = branchResourceSchema(r)
	})

	Describe("Metadata", func() {
		It("derives the type name from the provider type name", func() {
			req := resource.MetadataRequest{ProviderTypeName: "git"}
			resp := &resource.MetadataResponse{}

			r.Metadata(context.Background(), req, resp)

			Expect(resp.TypeName).To(Equal("git_branch"))
		})
	})

	Describe("Configure", func() {
		var configurer resource.ResourceWithConfigure

		BeforeEach(func() {
			var ok bool
			configurer, ok = r.(resource.ResourceWithConfigure)
			Expect(ok).To(BeTrue(), "expected gitBranchResource to implement ResourceWithConfigure")
		})

		It("does nothing when ProviderData is nil", func() {
			req := resource.ConfigureRequest{ProviderData: nil}
			resp := &resource.ConfigureResponse{}

			Expect(func() {
				configurer.Configure(context.Background(), req, resp)
			}).NotTo(Panic())
			Expect(resp.Diagnostics.HasError()).To(BeFalse())
		})

		It("adds an error diagnostic when ProviderData is an unexpected type", func() {
			req := resource.ConfigureRequest{ProviderData: "not-a-provider-data"}
			resp := &resource.ConfigureResponse{}

			configurer.Configure(context.Background(), req, resp)

			Expect(resp.Diagnostics.HasError()).To(BeTrue())
		})
	})

	Describe("Schema", func() {
		It("produces a schema with no errors", func() {
			schemaResp := resource.SchemaResponse{}
			r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)

			Expect(schemaResp.Diagnostics.HasError()).To(BeFalse())
		})

		It("defines exactly the id, repository, name, base_ref, base_sha, and resolved_ref attributes", func() {
			Expect(branchSchema.Attributes).To(HaveLen(6))
			Expect(branchSchema.Attributes).To(HaveKey("id"))
			Expect(branchSchema.Attributes).To(HaveKey("repository"))
			Expect(branchSchema.Attributes).To(HaveKey("name"))
			Expect(branchSchema.Attributes).To(HaveKey("base_ref"))
			Expect(branchSchema.Attributes).To(HaveKey("base_sha"))
			Expect(branchSchema.Attributes).To(HaveKey("resolved_ref"))
		})

		Describe("id attribute", func() {
			It("is computed only", func() {
				a := branchSchema.Attributes["id"]
				Expect(a.IsRequired()).To(BeFalse())
				Expect(a.IsOptional()).To(BeFalse())
				Expect(a.IsComputed()).To(BeTrue())
			})
		})

		Describe("repository attribute", func() {
			It("is required and defined as a single nested object", func() {
				a := branchSchema.Attributes["repository"]
				Expect(a.IsRequired()).To(BeTrue())
				Expect(a.IsOptional()).To(BeFalse())

				_, ok := a.(rschema.NestedAttribute)
				Expect(ok).To(BeTrue(), "expected repository to be a nested attribute type")

				_, ok = a.(rschema.SingleNestedAttribute)
				Expect(ok).To(BeTrue(), "expected repository to be a schema.SingleNestedAttribute")
			})

			repoAttrs := func() map[string]rschema.Attribute {
				single, ok := branchSchema.Attributes["repository"].(rschema.SingleNestedAttribute)
				Expect(ok).To(BeTrue(), "expected repository to be a schema.SingleNestedAttribute")
				return single.Attributes
			}

			It("defines a required url with a RequiresReplace plan modifier", func() {
				urlAttr, ok := repoAttrs()["url"]
				Expect(ok).To(BeTrue(), "expected repository to define a nested url attribute")
				Expect(urlAttr.IsRequired()).To(BeTrue())
				Expect(urlAttr.IsOptional()).To(BeFalse())

				strAttr, ok := urlAttr.(rschema.StringAttribute)
				Expect(ok).To(BeTrue(), "expected repository.url to be a schema.StringAttribute")
				Expect(strAttr.PlanModifiers).NotTo(BeEmpty(), "expected repository.url to have a RequiresReplace plan modifier")
			})

			It("defines an optional host with a validator restricting it to known host types", func() {
				hostAttr, ok := repoAttrs()["host"]
				Expect(ok).To(BeTrue(), "expected repository to define a nested host attribute")
				Expect(hostAttr.IsRequired()).To(BeFalse())
				Expect(hostAttr.IsOptional()).To(BeTrue())

				strAttr, ok := hostAttr.(rschema.StringAttribute)
				Expect(ok).To(BeTrue(), "expected repository.host to be a schema.StringAttribute")
				Expect(strAttr.Validators).NotTo(BeEmpty(), "expected repository.host to have at least one validator (e.g. stringvalidator.OneOf)")
			})

			It("defines an optional auth nested attribute with an optional, sensitive token child", func() {
				authAttr, ok := repoAttrs()["auth"]
				Expect(ok).To(BeTrue(), "expected repository to define a nested auth attribute")
				Expect(authAttr.IsRequired()).To(BeFalse())
				Expect(authAttr.IsOptional()).To(BeTrue())

				nested, ok := authAttr.(rschema.NestedAttribute)
				Expect(ok).To(BeTrue(), "expected repository.auth to be a nested attribute type")

				tokenAttr, ok := nested.GetNestedObject().GetAttributes()["token"]
				Expect(ok).To(BeTrue(), "expected repository.auth to define a nested token attribute")
				Expect(tokenAttr.IsRequired()).To(BeFalse())
				Expect(tokenAttr.IsOptional()).To(BeTrue())
				Expect(tokenAttr.IsSensitive()).To(BeTrue())
			})
		})

		Describe("name attribute", func() {
			It("is required with a RequiresReplace plan modifier", func() {
				a := branchSchema.Attributes["name"]
				Expect(a.IsRequired()).To(BeTrue())
				Expect(a.IsOptional()).To(BeFalse())
				Expect(a.IsComputed()).To(BeFalse())

				strAttr, ok := a.(rschema.StringAttribute)
				Expect(ok).To(BeTrue(), "expected name to be a schema.StringAttribute")
				Expect(strAttr.PlanModifiers).NotTo(BeEmpty(), "expected name to have a RequiresReplace plan modifier")
			})
		})

		Describe("base_ref attribute", func() {
			It("is required", func() {
				a := branchSchema.Attributes["base_ref"]
				Expect(a.IsRequired()).To(BeTrue())
				Expect(a.IsOptional()).To(BeFalse())
				Expect(a.IsComputed()).To(BeFalse())
			})
		})

		Describe("base_sha attribute", func() {
			It("is computed only", func() {
				a := branchSchema.Attributes["base_sha"]
				Expect(a.IsRequired()).To(BeFalse())
				Expect(a.IsOptional()).To(BeFalse())
				Expect(a.IsComputed()).To(BeTrue())
			})
		})

		Describe("resolved_ref attribute", func() {
			It("is computed only", func() {
				a := branchSchema.Attributes["resolved_ref"]
				Expect(a.IsRequired()).To(BeFalse())
				Expect(a.IsOptional()).To(BeFalse())
				Expect(a.IsComputed()).To(BeTrue())
			})
		})
	})

	Describe("Create", func() {
		const (
			repoURL = "https://example.com/repo.git"
			refName = "main"
			hash    = "abc123abc123abc123abc123abc123abc123ab"
		)

		configModel := func() branchModel {
			return branchModel{
				Id:          types.StringUnknown(),
				Repository:  branchRepoModel{Url: types.StringValue(repoURL), Host: types.StringNull(), Auth: nil},
				Name:        types.StringValue(refName),
				BaseRef:     types.StringValue(refName),
				BaseSha:     types.StringUnknown(),
				ResolvedRef: types.StringUnknown(),
			}
		}

		Context("when the base ref resolves successfully", func() {
			It("sets base_sha, resolved_ref, and id, and writes state", func() {
				fake := &fakeGitClient{
					lsRemoteFunc: func(ctx context.Context, url string, auth git.Auth) ([]git.Ref, error) {
						return []git.Ref{{Name: "refs/heads/main", Hash: hash}}, nil
					},
				}
				branchR := newBranchResourceWithClient(fake)
				s := branchResourceSchema(branchR)

				req := resource.CreateRequest{Config: buildBranchConfig(s, configModel())}
				resp := &resource.CreateResponse{State: tfsdk.State{Schema: s}}

				branchR.Create(context.Background(), req, resp)

				Expect(resp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", resp.Diagnostics))

				var got branchModel
				Expect(resp.State.Get(context.Background(), &got).HasError()).To(BeFalse())
				Expect(got.BaseSha.ValueString()).To(Equal(hash))
				Expect(got.ResolvedRef.ValueString()).To(Equal(hash))
				Expect(got.Id.ValueString()).To(Equal(repoURL + "#" + refName))

				Expect(fake.gotURL).To(Equal(repoURL))
			})
		})

		Context("when the base ref fails to resolve", func() {
			It("adds an error diagnostic without panicking", func() {
				fake := &fakeGitClient{
					lsRemoteFunc: func(ctx context.Context, url string, auth git.Auth) ([]git.Ref, error) {
						return nil, fmt.Errorf("boom")
					},
				}
				branchR := newBranchResourceWithClient(fake)
				s := branchResourceSchema(branchR)

				req := resource.CreateRequest{Config: buildBranchConfig(s, configModel())}
				resp := &resource.CreateResponse{State: tfsdk.State{Schema: s}}

				Expect(func() {
					branchR.Create(context.Background(), req, resp)
				}).NotTo(Panic())

				Expect(resp.Diagnostics.HasError()).To(BeTrue())
			})
		})
	})

	Describe("Read", func() {
		const (
			repoURL = "https://example.com/repo.git"
			refName = "main"
			oldHash = "0000000000000000000000000000000000000a"
			newHash = "1111111111111111111111111111111111111b"
		)

		stateModel := func(base string) branchModel {
			return branchModel{
				Id:          types.StringValue(repoURL + "#" + refName),
				Repository:  branchRepoModel{Url: types.StringValue(repoURL), Host: types.StringNull(), Auth: nil},
				Name:        types.StringValue(refName),
				BaseRef:     types.StringValue(refName),
				BaseSha:     types.StringValue(base),
				ResolvedRef: types.StringValue(base),
			}
		}

		Context("when the base ref has drifted to a new hash", func() {
			It("updates base_sha and resolved_ref to the newly resolved hash", func() {
				fake := &fakeGitClient{
					lsRemoteFunc: func(ctx context.Context, url string, auth git.Auth) ([]git.Ref, error) {
						return []git.Ref{{Name: "refs/heads/main", Hash: newHash}}, nil
					},
				}
				branchR := newBranchResourceWithClient(fake)
				s := branchResourceSchema(branchR)

				initial := buildBranchState(s, stateModel(oldHash))
				req := resource.ReadRequest{State: initial}
				resp := &resource.ReadResponse{State: tfsdk.State{Schema: s, Raw: initial.Raw}}

				branchR.Read(context.Background(), req, resp)

				Expect(resp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", resp.Diagnostics))

				var got branchModel
				Expect(resp.State.Get(context.Background(), &got).HasError()).To(BeFalse())
				Expect(got.BaseSha.ValueString()).To(Equal(newHash))
				Expect(got.ResolvedRef.ValueString()).To(Equal(newHash))
			})
		})

		Context("when the base ref no longer resolves", func() {
			It("removes the resource from state without an error diagnostic", func() {
				fake := &fakeGitClient{
					lsRemoteFunc: func(ctx context.Context, url string, auth git.Auth) ([]git.Ref, error) {
						return nil, fmt.Errorf("ref gone")
					},
				}
				branchR := newBranchResourceWithClient(fake)
				s := branchResourceSchema(branchR)

				initial := buildBranchState(s, stateModel(oldHash))
				req := resource.ReadRequest{State: initial}
				resp := &resource.ReadResponse{State: tfsdk.State{Schema: s, Raw: initial.Raw}}

				branchR.Read(context.Background(), req, resp)

				Expect(resp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", resp.Diagnostics))
				Expect(resp.State.Raw.IsNull()).To(BeTrue())
			})
		})
	})

	Describe("Update", func() {
		const (
			repoURL = "https://example.com/repo.git"
			refName = "main"
			hash    = "2222222222222222222222222222222222222c"
		)

		planModel := func() branchModel {
			return branchModel{
				Id:          types.StringUnknown(),
				Repository:  branchRepoModel{Url: types.StringValue(repoURL), Host: types.StringNull(), Auth: nil},
				Name:        types.StringValue(refName),
				BaseRef:     types.StringValue(refName),
				BaseSha:     types.StringUnknown(),
				ResolvedRef: types.StringUnknown(),
			}
		}

		Context("when the base ref resolves successfully", func() {
			It("sets base_sha, resolved_ref, and id from the plan, and writes state", func() {
				fake := &fakeGitClient{
					lsRemoteFunc: func(ctx context.Context, url string, auth git.Auth) ([]git.Ref, error) {
						return []git.Ref{{Name: "refs/heads/main", Hash: hash}}, nil
					},
				}
				branchR := newBranchResourceWithClient(fake)
				s := branchResourceSchema(branchR)

				req := resource.UpdateRequest{Plan: buildBranchPlan(s, planModel())}
				resp := &resource.UpdateResponse{State: tfsdk.State{Schema: s}}

				branchR.Update(context.Background(), req, resp)

				Expect(resp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", resp.Diagnostics))

				var got branchModel
				Expect(resp.State.Get(context.Background(), &got).HasError()).To(BeFalse())
				Expect(got.BaseSha.ValueString()).To(Equal(hash))
				Expect(got.ResolvedRef.ValueString()).To(Equal(hash))
				Expect(got.Id.ValueString()).To(Equal(repoURL + "#" + refName))
			})
		})

		Context("when the base ref fails to resolve", func() {
			It("adds an error diagnostic without panicking", func() {
				fake := &fakeGitClient{
					lsRemoteFunc: func(ctx context.Context, url string, auth git.Auth) ([]git.Ref, error) {
						return nil, fmt.Errorf("boom")
					},
				}
				branchR := newBranchResourceWithClient(fake)
				s := branchResourceSchema(branchR)

				req := resource.UpdateRequest{Plan: buildBranchPlan(s, planModel())}
				resp := &resource.UpdateResponse{State: tfsdk.State{Schema: s}}

				Expect(func() {
					branchR.Update(context.Background(), req, resp)
				}).NotTo(Panic())

				Expect(resp.Diagnostics.HasError()).To(BeTrue())
			})
		})
	})

	Describe("Delete", func() {
		It("is a no-op that never adds an error diagnostic", func() {
			fake := &fakeGitClient{}
			branchR := newBranchResourceWithClient(fake)
			s := branchResourceSchema(branchR)

			model := branchModel{
				Id:          types.StringValue("https://example.com/repo.git#main"),
				Repository:  branchRepoModel{Url: types.StringValue("https://example.com/repo.git"), Host: types.StringNull(), Auth: nil},
				Name:        types.StringValue("main"),
				BaseRef:     types.StringValue("main"),
				BaseSha:     types.StringValue("abc123abc123abc123abc123abc123abc123ab"),
				ResolvedRef: types.StringValue("abc123abc123abc123abc123abc123abc123ab"),
			}

			req := resource.DeleteRequest{State: buildBranchState(s, model)}
			resp := &resource.DeleteResponse{}

			Expect(func() {
				branchR.Delete(context.Background(), req, resp)
			}).NotTo(Panic())
			Expect(resp.Diagnostics.HasError()).To(BeFalse())
		})
	})

	Describe("ImportState", func() {
		const (
			repoURL = "https://example.com/repo.git"
			refName = "main"
			hash    = "3333333333333333333333333333333333333d"
		)

		Context("with a malformed import ID (no #)", func() {
			It("adds an 'Invalid Import ID' error diagnostic", func() {
				importer, ok := r.(resource.ResourceWithImportState)
				Expect(ok).To(BeTrue(), "expected gitBranchResource to implement ResourceWithImportState")

				req := resource.ImportStateRequest{ID: "no-hash-here"}
				resp := &resource.ImportStateResponse{State: tfsdk.State{Schema: branchSchema}}

				importer.ImportState(context.Background(), req, resp)

				Expect(resp.Diagnostics.HasError()).To(BeTrue())

				foundSummary := false
				for _, d := range resp.Diagnostics {
					if d.Summary() == "Invalid Import ID" {
						foundSummary = true
					}
				}
				Expect(foundSummary).To(BeTrue(), fmt.Sprintf("expected an 'Invalid Import ID' diagnostic, got: %v", resp.Diagnostics))
			})
		})

		Context("with a well-formed import ID", func() {
			It("resolves the ref unauthenticated and populates state", func() {
				fake := &fakeGitClient{
					lsRemoteFunc: func(ctx context.Context, url string, auth git.Auth) ([]git.Ref, error) {
						return []git.Ref{{Name: "refs/heads/main", Hash: hash}}, nil
					},
				}
				branchR := newBranchResourceWithClient(fake)
				s := branchResourceSchema(branchR)
				importer, ok := branchR.(resource.ResourceWithImportState)
				Expect(ok).To(BeTrue(), "expected gitBranchResource to implement ResourceWithImportState")

				req := resource.ImportStateRequest{ID: repoURL + "#" + refName}
				resp := &resource.ImportStateResponse{State: tfsdk.State{Schema: s}}

				importer.ImportState(context.Background(), req, resp)

				Expect(resp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", resp.Diagnostics))

				var got branchModel
				Expect(resp.State.Get(context.Background(), &got).HasError()).To(BeFalse())
				Expect(got.Id.ValueString()).To(Equal(repoURL + "#" + refName))
				Expect(got.Repository.Url.ValueString()).To(Equal(repoURL))
				Expect(got.Name.ValueString()).To(Equal(refName))
				Expect(got.BaseRef.ValueString()).To(Equal(refName))
				Expect(got.BaseSha.ValueString()).To(Equal(hash))
				Expect(got.ResolvedRef.ValueString()).To(Equal(hash))
				Expect(got.Repository.Host.IsNull()).To(BeTrue())
				Expect(got.Repository.Auth).To(BeNil())

				Expect(fake.gotAuth).To(Equal(git.Auth{}))
			})

			It("splits on the last # in the import ID", func() {
				const nameWithHash = "weird#name"
				fake := &fakeGitClient{
					lsRemoteFunc: func(ctx context.Context, url string, auth git.Auth) ([]git.Ref, error) {
						return []git.Ref{{Name: "refs/heads/" + nameWithHash, Hash: hash}}, nil
					},
				}
				branchR := newBranchResourceWithClient(fake)
				s := branchResourceSchema(branchR)
				importer, ok := branchR.(resource.ResourceWithImportState)
				Expect(ok).To(BeTrue())

				importID := repoURL + "#" + nameWithHash
				Expect(strings.Count(importID, "#")).To(BeNumerically(">", 1))

				req := resource.ImportStateRequest{ID: importID}
				resp := &resource.ImportStateResponse{State: tfsdk.State{Schema: s}}

				importer.ImportState(context.Background(), req, resp)

				Expect(resp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", resp.Diagnostics))

				var got branchModel
				Expect(resp.State.Get(context.Background(), &got).HasError()).To(BeFalse())
				Expect(got.Repository.Url.ValueString()).To(Equal(repoURL))
				Expect(got.Name.ValueString()).To(Equal(nameWithHash))
			})
		})
	})
})

// testAccProtoV6ProviderFactories is already declared in
// git_repository_data_source_test.go for this package; the acceptance test
// below reuses it directly.
func TestAccGitBranch_basic(t *testing.T) {
	repoDir := newTestRepo(t)
	repoURL := "file://" + repoDir

	// newTestRepo doesn't pin init.defaultBranch, so the default branch name
	// depends on the host's git configuration (commonly "main" or "master").
	// Ask the repo itself rather than hardcoding a name.
	out, err := exec.Command("git", "-C", repoDir, "branch", "--show-current").Output()
	if err != nil {
		t.Fatalf("determining default branch of test repo: %v", err)
	}
	defaultBranch := strings.TrimSpace(string(out))
	if defaultBranch == "" {
		t.Fatalf("could not determine default branch of test repo")
	}

	tfresource.Test(t, tfresource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []tfresource.TestStep{
			{
				Config: fmt.Sprintf(`resource "git_branch" "test" {
  repository = {
    url = %[1]q
  }
  name     = %[2]q
  base_ref = %[2]q
}`, repoURL, defaultBranch),
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr("git_branch.test", "name", defaultBranch),
					tfresource.TestCheckResourceAttr("git_branch.test", "base_ref", defaultBranch),
					tfresource.TestCheckResourceAttrSet("git_branch.test", "base_sha"),
					tfresource.TestCheckResourceAttrSet("git_branch.test", "resolved_ref"),
					tfresource.TestCheckResourceAttrSet("git_branch.test", "id"),
				),
			},
		},
	})
}
