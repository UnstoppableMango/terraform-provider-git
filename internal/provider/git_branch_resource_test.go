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
	tfterraform "github.com/hashicorp/terraform-plugin-testing/terraform"
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
	Patches     types.List      `tfsdk:"patches"`
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
	lsRemoteFunc     func(ctx context.Context, url string, auth git.Auth) ([]git.Ref, error)
	applyPatchesFunc func(ctx context.Context, req git.ApplyPatchesRequest) (git.ApplyPatchesResult, error)

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

func (f *fakeGitClient) ApplyPatches(ctx context.Context, req git.ApplyPatchesRequest) (git.ApplyPatchesResult, error) {
	if f.applyPatchesFunc == nil {
		panic("fakeGitClient: ApplyPatches called but applyPatchesFunc is nil")
	}
	return f.applyPatchesFunc(ctx, req)
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

		It("defines exactly the id, repository, name, base_ref, base_sha, resolved_ref, and patches attributes", func() {
			Expect(branchSchema.Attributes).To(HaveLen(7))
			Expect(branchSchema.Attributes).To(HaveKey("id"))
			Expect(branchSchema.Attributes).To(HaveKey("repository"))
			Expect(branchSchema.Attributes).To(HaveKey("name"))
			Expect(branchSchema.Attributes).To(HaveKey("base_ref"))
			Expect(branchSchema.Attributes).To(HaveKey("base_sha"))
			Expect(branchSchema.Attributes).To(HaveKey("resolved_ref"))
			Expect(branchSchema.Attributes).To(HaveKey("patches"))
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

		Describe("patches attribute", func() {
			It("is an optional list of strings", func() {
				a := branchSchema.Attributes["patches"]
				Expect(a.IsRequired()).To(BeFalse())
				Expect(a.IsOptional()).To(BeTrue())
				Expect(a.IsComputed()).To(BeFalse())

				listAttr, ok := a.(rschema.ListAttribute)
				Expect(ok).To(BeTrue(), "expected patches to be a schema.ListAttribute")
				Expect(listAttr.ElementType).To(Equal(types.StringType))
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
				Patches:     types.ListNull(types.StringType),
			}
		}

		configModelWithPatches := func(patches []string) branchModel {
			m := configModel()
			list, diags := types.ListValueFrom(context.Background(), types.StringType, patches)
			Expect(diags.HasError()).To(BeFalse())
			m.Patches = list
			return m
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

		Context("when patches are set", func() {
			It("calls ApplyPatches with the resolved base sha and writes the returned resolved_ref to state", func() {
				const resolvedSHA = "5555555555555555555555555555555555555f"
				patches := []string{"diff --git a/x b/x", "diff --git a/y b/y"}

				var gotReq git.ApplyPatchesRequest
				fake := &fakeGitClient{
					lsRemoteFunc: func(ctx context.Context, url string, auth git.Auth) ([]git.Ref, error) {
						return []git.Ref{{Name: "refs/heads/main", Hash: hash}}, nil
					},
					applyPatchesFunc: func(ctx context.Context, req git.ApplyPatchesRequest) (git.ApplyPatchesResult, error) {
						gotReq = req
						return git.ApplyPatchesResult{ResolvedSHA: resolvedSHA}, nil
					},
				}
				branchR := newBranchResourceWithClient(fake)
				s := branchResourceSchema(branchR)

				req := resource.CreateRequest{Config: buildBranchConfig(s, configModelWithPatches(patches))}
				resp := &resource.CreateResponse{State: tfsdk.State{Schema: s}}

				branchR.Create(context.Background(), req, resp)

				Expect(resp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", resp.Diagnostics))

				Expect(gotReq.URL).To(Equal(repoURL))
				Expect(gotReq.Branch).To(Equal(refName))
				Expect(gotReq.BaseRef).To(Equal(hash))
				Expect(gotReq.Patches).To(Equal(patches))

				var got branchModel
				Expect(resp.State.Get(context.Background(), &got).HasError()).To(BeFalse())
				Expect(got.ResolvedRef.ValueString()).To(Equal(resolvedSHA))
				Expect(got.BaseSha.ValueString()).To(Equal(hash))
				Expect(got.Id.ValueString()).To(Equal(repoURL + "#" + refName))
			})
		})

		Context("when patches are set and ApplyPatches fails", func() {
			It("adds an error diagnostic without panicking and does not write state", func() {
				patches := []string{"diff --git a/x b/x"}
				fake := &fakeGitClient{
					lsRemoteFunc: func(ctx context.Context, url string, auth git.Auth) ([]git.Ref, error) {
						return []git.Ref{{Name: "refs/heads/main", Hash: hash}}, nil
					},
					applyPatchesFunc: func(ctx context.Context, req git.ApplyPatchesRequest) (git.ApplyPatchesResult, error) {
						return git.ApplyPatchesResult{}, fmt.Errorf("apply failed")
					},
				}
				branchR := newBranchResourceWithClient(fake)
				s := branchResourceSchema(branchR)

				req := resource.CreateRequest{Config: buildBranchConfig(s, configModelWithPatches(patches))}
				resp := &resource.CreateResponse{State: tfsdk.State{Schema: s}}

				Expect(func() {
					branchR.Create(context.Background(), req, resp)
				}).NotTo(Panic())

				Expect(resp.Diagnostics.HasError()).To(BeTrue())
				Expect(resp.State.Raw.IsNull()).To(BeTrue())
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
				Patches:     types.ListNull(types.StringType),
			}
		}

		stateModelWithPatches := func(base string, patches []string) branchModel {
			m := stateModel(base)
			list, diags := types.ListValueFrom(context.Background(), types.StringType, patches)
			Expect(diags.HasError()).To(BeFalse())
			m.Patches = list
			return m
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

		Context("when patches are set in state", func() {
			It("never calls ApplyPatches and refreshes resolved_ref from the branch's current tip on the remote", func() {
				fake := &fakeGitClient{
					lsRemoteFunc: func(ctx context.Context, url string, auth git.Auth) ([]git.Ref, error) {
						// base_ref and the branch name are both "main" in this
						// Describe block, so the same ref list resolves both
						// the base ref lookup and the branch-tip lookup.
						return []git.Ref{{Name: "refs/heads/main", Hash: newHash}}, nil
					},
					// applyPatchesFunc intentionally left nil: if Read calls
					// ApplyPatches it will panic and fail this test.
				}
				branchR := newBranchResourceWithClient(fake)
				s := branchResourceSchema(branchR)

				initial := buildBranchState(s, stateModelWithPatches(oldHash, []string{"diff --git a b"}))
				req := resource.ReadRequest{State: initial}
				resp := &resource.ReadResponse{State: tfsdk.State{Schema: s, Raw: initial.Raw}}

				Expect(func() {
					branchR.Read(context.Background(), req, resp)
				}).NotTo(Panic())

				Expect(resp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", resp.Diagnostics))

				var got branchModel
				Expect(resp.State.Get(context.Background(), &got).HasError()).To(BeFalse())
				Expect(got.BaseSha.ValueString()).To(Equal(newHash))
				Expect(got.ResolvedRef.ValueString()).To(Equal(newHash))
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
				Patches:     types.ListNull(types.StringType),
			}
		}

		planModelWithPatches := func(patches []string) branchModel {
			m := planModel()
			list, diags := types.ListValueFrom(context.Background(), types.StringType, patches)
			Expect(diags.HasError()).To(BeFalse())
			m.Patches = list
			return m
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

		Context("when patches are set", func() {
			It("calls ApplyPatches with the resolved base sha and writes the returned resolved_ref to state", func() {
				const resolvedSHA = "4444444444444444444444444444444444444e"
				patches := []string{"diff --git a/x b/x", "diff --git a/y b/y"}

				var gotReq git.ApplyPatchesRequest
				fake := &fakeGitClient{
					lsRemoteFunc: func(ctx context.Context, url string, auth git.Auth) ([]git.Ref, error) {
						return []git.Ref{{Name: "refs/heads/main", Hash: hash}}, nil
					},
					applyPatchesFunc: func(ctx context.Context, req git.ApplyPatchesRequest) (git.ApplyPatchesResult, error) {
						gotReq = req
						return git.ApplyPatchesResult{ResolvedSHA: resolvedSHA}, nil
					},
				}
				branchR := newBranchResourceWithClient(fake)
				s := branchResourceSchema(branchR)

				req := resource.UpdateRequest{Plan: buildBranchPlan(s, planModelWithPatches(patches))}
				resp := &resource.UpdateResponse{State: tfsdk.State{Schema: s}}

				branchR.Update(context.Background(), req, resp)

				Expect(resp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", resp.Diagnostics))

				Expect(gotReq.URL).To(Equal(repoURL))
				Expect(gotReq.Branch).To(Equal(refName))
				Expect(gotReq.BaseRef).To(Equal(hash))
				Expect(gotReq.Patches).To(Equal(patches))

				var got branchModel
				Expect(resp.State.Get(context.Background(), &got).HasError()).To(BeFalse())
				Expect(got.ResolvedRef.ValueString()).To(Equal(resolvedSHA))
				Expect(got.BaseSha.ValueString()).To(Equal(hash))
			})
		})

		Context("when patches are set and ApplyPatches fails", func() {
			It("adds an error diagnostic without panicking", func() {
				patches := []string{"diff --git a/x b/x"}
				fake := &fakeGitClient{
					lsRemoteFunc: func(ctx context.Context, url string, auth git.Auth) ([]git.Ref, error) {
						return []git.Ref{{Name: "refs/heads/main", Hash: hash}}, nil
					},
					applyPatchesFunc: func(ctx context.Context, req git.ApplyPatchesRequest) (git.ApplyPatchesResult, error) {
						return git.ApplyPatchesResult{}, fmt.Errorf("apply failed")
					},
				}
				branchR := newBranchResourceWithClient(fake)
				s := branchResourceSchema(branchR)

				req := resource.UpdateRequest{Plan: buildBranchPlan(s, planModelWithPatches(patches))}
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
				Patches:     types.ListNull(types.StringType),
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
				Expect(got.Patches.IsNull()).To(BeTrue())

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

// TestAccGitBranch_patches exercises the patches attribute end to end: it
// applies a single-file-add patch on top of the test repo's default branch
// and expects resolved_ref to differ from base_sha (the patch produced a new
// commit that was force-pushed to the branch).
//
// Both the exec and gogit backends implement ApplyPatches; this test pins
// the exec backend explicitly to keep exec covered by an acceptance test in
// addition to gogit's own unit tests in internal/git/gogit.
func TestAccGitBranch_patches(t *testing.T) {
	repoDir := newTestRepo(t)
	repoURL := "file://" + repoDir

	out, err := exec.Command("git", "-C", repoDir, "branch", "--show-current").Output()
	if err != nil {
		t.Fatalf("determining default branch of test repo: %v", err)
	}
	defaultBranch := strings.TrimSpace(string(out))
	if defaultBranch == "" {
		t.Fatalf("could not determine default branch of test repo")
	}

	const patch = `diff --git a/PATCH.md b/PATCH.md
new file mode 100644
--- /dev/null
+++ b/PATCH.md
@@ -0,0 +1 @@
+patched
`

	tfresource.Test(t, tfresource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []tfresource.TestStep{
			{
				Config: fmt.Sprintf(`provider "git" {
  git_implementation = "exec"
}

resource "git_branch" "test" {
  repository = {
    url = %[1]q
  }
  name     = %[2]q
  base_ref = %[2]q
  patches  = [%[3]q]
}`, repoURL, defaultBranch, patch),
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr("git_branch.test", "name", defaultBranch),
					tfresource.TestCheckResourceAttr("git_branch.test", "base_ref", defaultBranch),
					tfresource.TestCheckResourceAttrSet("git_branch.test", "base_sha"),
					tfresource.TestCheckResourceAttrSet("git_branch.test", "resolved_ref"),
					func(s *tfterraform.State) error {
						rs, ok := s.RootModule().Resources["git_branch.test"]
						if !ok {
							return fmt.Errorf("git_branch.test not found in state")
						}
						baseSha := rs.Primary.Attributes["base_sha"]
						resolvedRef := rs.Primary.Attributes["resolved_ref"]
						if baseSha == resolvedRef {
							return fmt.Errorf("expected resolved_ref (%s) to differ from base_sha (%s) after applying patches", resolvedRef, baseSha)
						}
						return nil
					},
				),
			},
		},
	})
}
