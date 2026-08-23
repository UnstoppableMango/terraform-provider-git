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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/defaults"
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
// gitBranchResourceModel/gitBranchRepositoryModel, letting this external test
// package build tfsdk.Config/Plan/State values without access to those types.
type branchModel struct {
	Id          types.String    `tfsdk:"id"`
	Repository  branchRepoModel `tfsdk:"repository"`
	Name        types.String    `tfsdk:"name"`
	BaseRef     types.String    `tfsdk:"base_ref"`
	BaseSha     types.String    `tfsdk:"base_sha"`
	ResolvedRef types.String    `tfsdk:"resolved_ref"`
	Patches     types.List      `tfsdk:"patches"`
	OnConflict  types.String    `tfsdk:"on_conflict"`
}

type branchRepoModel struct {
	Url  types.String `tfsdk:"url"`
	Host types.String `tfsdk:"host"`
	Auth *authModel   `tfsdk:"auth"`
}

// fakeGitClient is a test double for git.Client: each method is backed by a
// configurable function field, and a nil field panics if called.
type fakeGitClient struct {
	lsRemoteFunc     func(ctx context.Context, url string, auth git.Auth) ([]git.Ref, error)
	applyPatchesFunc func(ctx context.Context, req git.ApplyPatchesRequest) (git.ApplyPatchesResult, error)
	isAncestorFunc   func(ctx context.Context, url string, auth git.Auth, ancestor, descendant string) (bool, error)

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

func (f *fakeGitClient) IsAncestor(ctx context.Context, url string, auth git.Auth, ancestor, descendant string) (bool, error) {
	if f.isAncestorFunc == nil {
		panic("fakeGitClient: IsAncestor called but isAncestorFunc is nil")
	}
	return f.isAncestorFunc(ctx, url, auth, ancestor, descendant)
}

// newBranchResourceWithClient returns a git_branch resource.Resource with its
// unexported client field set to client, via reflection, since the concrete
// type and its field are otherwise out of reach from this external test package.
func newBranchResourceWithClient(client git.Client) resource.Resource {
	r := provider.NewGitBranchResource()

	v := reflect.ValueOf(r)
	if v.Kind() == reflect.Pointer {
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
// Set method (unlike Plan/State), so the raw value is built via a throwaway State.
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

		It("defines exactly the id, repository, name, base_ref, base_sha, resolved_ref, patches, and on_conflict attributes", func() {
			Expect(branchSchema.Attributes).To(HaveLen(8))
			Expect(branchSchema.Attributes).To(HaveKey("id"))
			Expect(branchSchema.Attributes).To(HaveKey("repository"))
			Expect(branchSchema.Attributes).To(HaveKey("name"))
			Expect(branchSchema.Attributes).To(HaveKey("base_ref"))
			Expect(branchSchema.Attributes).To(HaveKey("base_sha"))
			Expect(branchSchema.Attributes).To(HaveKey("resolved_ref"))
			Expect(branchSchema.Attributes).To(HaveKey("patches"))
			Expect(branchSchema.Attributes).To(HaveKey("on_conflict"))
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

		Describe("on_conflict attribute", func() {
			It("is an optional, computed string defaulting to \"force\" with a fail/force validator", func() {
				a := branchSchema.Attributes["on_conflict"]
				Expect(a.IsRequired()).To(BeFalse())
				Expect(a.IsOptional()).To(BeTrue())
				Expect(a.IsComputed()).To(BeTrue())

				strAttr, ok := a.(rschema.StringAttribute)
				Expect(ok).To(BeTrue(), "expected on_conflict to be a schema.StringAttribute")
				Expect(strAttr.Validators).NotTo(BeEmpty(), "expected on_conflict to have at least one validator (e.g. stringvalidator.OneOf)")
				Expect(strAttr.Default).NotTo(BeNil(), "expected on_conflict to have a Default")

				var defaultResp defaults.StringResponse
				strAttr.Default.DefaultString(context.Background(), defaults.StringRequest{}, &defaultResp)
				Expect(defaultResp.Diagnostics.HasError()).To(BeFalse())
				Expect(defaultResp.PlanValue.ValueString()).To(Equal("force"))
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
				// Left null, as it would be in a real Config when the user
				// omits on_conflict; Create is expected to fill in the
				// "force" default itself (see the "on_conflict" Context
				// below).
				OnConflict: types.StringNull(),
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
				Expect(got.OnConflict.ValueString()).To(Equal("force"))

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
				OnConflict:  types.StringValue("force"),
			}
		}

		stateModelWithPatches := func(base string, patches []string) branchModel {
			m := stateModel(base)
			list, diags := types.ListValueFrom(context.Background(), types.StringType, patches)
			Expect(diags.HasError()).To(BeFalse())
			m.Patches = list
			return m
		}

		Context("when the base ref has drifted to a new hash that fast-forwards from the old one", func() {
			It("updates base_sha and resolved_ref to the newly resolved hash without a warning", func() {
				var gotAncestor, gotDescendant string
				fake := &fakeGitClient{
					lsRemoteFunc: func(ctx context.Context, url string, auth git.Auth) ([]git.Ref, error) {
						return []git.Ref{{Name: "refs/heads/main", Hash: newHash}}, nil
					},
					isAncestorFunc: func(ctx context.Context, url string, auth git.Auth, ancestor, descendant string) (bool, error) {
						gotAncestor, gotDescendant = ancestor, descendant
						return true, nil
					},
				}
				branchR := newBranchResourceWithClient(fake)
				s := branchResourceSchema(branchR)

				initial := buildBranchState(s, stateModel(oldHash))
				req := resource.ReadRequest{State: initial}
				resp := &resource.ReadResponse{State: tfsdk.State{Schema: s, Raw: initial.Raw}}

				branchR.Read(context.Background(), req, resp)

				Expect(resp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", resp.Diagnostics))
				Expect(resp.Diagnostics.WarningsCount()).To(Equal(0))
				Expect(gotAncestor).To(Equal(oldHash))
				Expect(gotDescendant).To(Equal(newHash))

				var got branchModel
				Expect(resp.State.Get(context.Background(), &got).HasError()).To(BeFalse())
				Expect(got.BaseSha.ValueString()).To(Equal(newHash))
				Expect(got.ResolvedRef.ValueString()).To(Equal(newHash))
			})
		})

		Context("when the base ref has drifted to a new hash that is not a fast-forward of the old one", func() {
			It("adds a warning diagnostic but still updates base_sha and resolved_ref", func() {
				fake := &fakeGitClient{
					lsRemoteFunc: func(ctx context.Context, url string, auth git.Auth) ([]git.Ref, error) {
						return []git.Ref{{Name: "refs/heads/main", Hash: newHash}}, nil
					},
					isAncestorFunc: func(ctx context.Context, url string, auth git.Auth, ancestor, descendant string) (bool, error) {
						return false, nil
					},
				}
				branchR := newBranchResourceWithClient(fake)
				s := branchResourceSchema(branchR)

				initial := buildBranchState(s, stateModel(oldHash))
				req := resource.ReadRequest{State: initial}
				resp := &resource.ReadResponse{State: tfsdk.State{Schema: s, Raw: initial.Raw}}

				branchR.Read(context.Background(), req, resp)

				Expect(resp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", resp.Diagnostics))
				Expect(resp.Diagnostics.WarningsCount()).To(Equal(1))

				var got branchModel
				Expect(resp.State.Get(context.Background(), &got).HasError()).To(BeFalse())
				Expect(got.BaseSha.ValueString()).To(Equal(newHash))
				Expect(got.ResolvedRef.ValueString()).To(Equal(newHash))
			})
		})

		Context("when checking base ref ancestry fails", func() {
			It("adds an error diagnostic", func() {
				fake := &fakeGitClient{
					lsRemoteFunc: func(ctx context.Context, url string, auth git.Auth) ([]git.Ref, error) {
						return []git.Ref{{Name: "refs/heads/main", Hash: newHash}}, nil
					},
					isAncestorFunc: func(ctx context.Context, url string, auth git.Auth, ancestor, descendant string) (bool, error) {
						return false, fmt.Errorf("transport failure")
					},
				}
				branchR := newBranchResourceWithClient(fake)
				s := branchResourceSchema(branchR)

				initial := buildBranchState(s, stateModel(oldHash))
				req := resource.ReadRequest{State: initial}
				resp := &resource.ReadResponse{State: tfsdk.State{Schema: s, Raw: initial.Raw}}

				branchR.Read(context.Background(), req, resp)

				Expect(resp.Diagnostics.HasError()).To(BeTrue())
			})
		})

		Context("when the base ref has not drifted", func() {
			It("never calls IsAncestor", func() {
				fake := &fakeGitClient{
					lsRemoteFunc: func(ctx context.Context, url string, auth git.Auth) ([]git.Ref, error) {
						return []git.Ref{{Name: "refs/heads/main", Hash: oldHash}}, nil
					},
					// isAncestorFunc intentionally left nil: if Read calls
					// IsAncestor it will panic and fail this test.
				}
				branchR := newBranchResourceWithClient(fake)
				s := branchResourceSchema(branchR)

				initial := buildBranchState(s, stateModel(oldHash))
				req := resource.ReadRequest{State: initial}
				resp := &resource.ReadResponse{State: tfsdk.State{Schema: s, Raw: initial.Raw}}

				Expect(func() {
					branchR.Read(context.Background(), req, resp)
				}).NotTo(Panic())

				Expect(resp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", resp.Diagnostics))
			})
		})

		Context("when the base ref no longer resolves", func() {
			It("removes the resource from state with a warning diagnostic explaining why", func() {
				fake := &fakeGitClient{
					lsRemoteFunc: func(ctx context.Context, url string, auth git.Auth) ([]git.Ref, error) {
						// Remote is reachable, but the ref itself is gone
						// (e.g. the branch was deleted) — this is the
						// genuine "not found" condition that should be
						// treated as a delete signal.
						return []git.Ref{{Name: "refs/heads/other", Hash: newHash}}, nil
					},
				}
				branchR := newBranchResourceWithClient(fake)
				s := branchResourceSchema(branchR)

				initial := buildBranchState(s, stateModel(oldHash))
				req := resource.ReadRequest{State: initial}
				resp := &resource.ReadResponse{State: tfsdk.State{Schema: s, Raw: initial.Raw}}

				branchR.Read(context.Background(), req, resp)

				Expect(resp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", resp.Diagnostics))
				Expect(resp.Diagnostics.WarningsCount()).To(Equal(1))
				Expect(resp.Diagnostics.Warnings()[0].Summary()).To(Equal("base_ref No Longer Resolves, Removing From State"))
				Expect(resp.State.Raw.IsNull()).To(BeTrue())
			})
		})

		Context("when base_ref no longer resolves but patches are set and the branch's own tip still resolves", func() {
			It("adds an error diagnostic and does not remove the resource from state", func() {
				fake := &fakeGitClient{
					lsRemoteFunc: func(ctx context.Context, url string, auth git.Auth) ([]git.Ref, error) {
						// base_ref ("gone-base") is missing, but the tracked
						// branch's own tip (refName) still resolves, so the
						// branch itself is not confirmed gone.
						return []git.Ref{{Name: "refs/heads/" + refName, Hash: newHash}}, nil
					},
				}
				branchR := newBranchResourceWithClient(fake)
				s := branchResourceSchema(branchR)

				m := stateModelWithPatches(oldHash, []string{"diff --git a b"})
				m.BaseRef = types.StringValue("gone-base")

				initial := buildBranchState(s, m)
				req := resource.ReadRequest{State: initial}
				resp := &resource.ReadResponse{State: tfsdk.State{Schema: s, Raw: initial.Raw}}

				branchR.Read(context.Background(), req, resp)

				Expect(resp.Diagnostics.HasError()).To(BeTrue())
				Expect(resp.Diagnostics.Errors()[0].Summary()).To(Equal("base_ref No Longer Resolves, Patches Configured"))
				Expect(resp.State.Raw.IsNull()).To(BeFalse())
			})
		})

		Context("when the branch's own tip no longer resolves (deleted upstream)", func() {
			It("removes the resource from state with a warning diagnostic explaining why", func() {
				fake := &fakeGitClient{
					lsRemoteFunc: func(ctx context.Context, url string, auth git.Auth) ([]git.Ref, error) {
						// base_ref ("gone-base") still resolves, but the
						// tracked branch's own tip (refName) is gone from
						// the remote's ref list.
						return []git.Ref{{Name: "refs/heads/gone-base", Hash: oldHash}}, nil
					},
				}
				branchR := newBranchResourceWithClient(fake)
				s := branchResourceSchema(branchR)

				m := stateModelWithPatches(oldHash, []string{"diff --git a b"})
				m.BaseRef = types.StringValue("gone-base")

				initial := buildBranchState(s, m)
				req := resource.ReadRequest{State: initial}
				resp := &resource.ReadResponse{State: tfsdk.State{Schema: s, Raw: initial.Raw}}

				branchR.Read(context.Background(), req, resp)

				Expect(resp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", resp.Diagnostics))
				Expect(resp.Diagnostics.WarningsCount()).To(Equal(1))
				Expect(resp.Diagnostics.Warnings()[0].Summary()).To(Equal("Branch No Longer Exists, Removing From State"))
				Expect(resp.State.Raw.IsNull()).To(BeTrue())
			})
		})

		Context("when listing remote refs fails for a reason other than the ref being gone", func() {
			It("adds an error diagnostic and does not remove the resource from state", func() {
				fake := &fakeGitClient{
					lsRemoteFunc: func(ctx context.Context, url string, auth git.Auth) ([]git.Ref, error) {
						return nil, fmt.Errorf("connection refused")
					},
				}
				branchR := newBranchResourceWithClient(fake)
				s := branchResourceSchema(branchR)

				initial := buildBranchState(s, stateModel(oldHash))
				req := resource.ReadRequest{State: initial}
				resp := &resource.ReadResponse{State: tfsdk.State{Schema: s, Raw: initial.Raw}}

				branchR.Read(context.Background(), req, resp)

				Expect(resp.Diagnostics.HasError()).To(BeTrue())
				Expect(resp.State.Raw.IsNull()).To(BeFalse())
			})
		})

		// DESIGN.md's "Auth revoked/expired between Read and Update" edge
		// case: an auth failure must not be mistaken for the ref being gone,
		// which would silently remove the resource from state. The
		// classification is by error type (refNotFoundError is only built
		// after LsRemote succeeds), never by matching the message text, and
		// these two specs pin that down using a message that reads exactly
		// like a missing ref.
		Context("when listing remote refs fails with an auth error whose message reads like a missing ref", func() {
			// What GitHub returns for a revoked or expired token: it masks a
			// 403 as a 404, so the text says "not found" while the ref itself
			// is fine.
			revokedTokenErr := func(url string) error {
				return fmt.Errorf(
					"ls-remote %s: exit status 128: remote: Repository not found. fatal: repository %q not found",
					url, url,
				)
			}

			It("adds an error diagnostic and does not remove the resource from state", func() {
				fake := &fakeGitClient{
					lsRemoteFunc: func(ctx context.Context, url string, auth git.Auth) ([]git.Ref, error) {
						return nil, revokedTokenErr(url)
					},
				}
				branchR := newBranchResourceWithClient(fake)
				s := branchResourceSchema(branchR)

				initial := buildBranchState(s, stateModel(oldHash))
				req := resource.ReadRequest{State: initial}
				resp := &resource.ReadResponse{State: tfsdk.State{Schema: s, Raw: initial.Raw}}

				branchR.Read(context.Background(), req, resp)

				Expect(resp.Diagnostics.HasError()).To(BeTrue())
				Expect(resp.Diagnostics.Errors()[0].Summary()).To(Equal("Unable to Read Branch"))
				Expect(resp.Diagnostics.WarningsCount()).To(Equal(0))
				Expect(resp.State.Raw.IsNull()).To(BeFalse())
			})

			It("does not remove the resource from state when patches are configured either", func() {
				// With patches set, a genuinely missing branch tip is the one
				// unconditional delete signal Read has; this covers that path
				// too, not just the no-patches base_ref path above.
				fake := &fakeGitClient{
					lsRemoteFunc: func(ctx context.Context, url string, auth git.Auth) ([]git.Ref, error) {
						return nil, revokedTokenErr(url)
					},
				}
				branchR := newBranchResourceWithClient(fake)
				s := branchResourceSchema(branchR)

				initial := buildBranchState(s, stateModelWithPatches(oldHash, []string{"diff --git a b"}))
				req := resource.ReadRequest{State: initial}
				resp := &resource.ReadResponse{State: tfsdk.State{Schema: s, Raw: initial.Raw}}

				branchR.Read(context.Background(), req, resp)

				Expect(resp.Diagnostics.HasError()).To(BeTrue())
				Expect(resp.Diagnostics.Errors()[0].Summary()).To(Equal("Unable to Read Branch"))
				Expect(resp.Diagnostics.WarningsCount()).To(Equal(0))
				Expect(resp.State.Raw.IsNull()).To(BeFalse())
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
					isAncestorFunc: func(ctx context.Context, url string, auth git.Auth, ancestor, descendant string) (bool, error) {
						return true, nil
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
				// Plan already has schema defaults applied by Terraform core,
				// so on_conflict is "force" here, not null.
				OnConflict: types.StringValue("force"),
			}
		}

		planModelWithPatches := func(patches []string) branchModel {
			m := planModel()
			list, diags := types.ListValueFrom(context.Background(), types.StringType, patches)
			Expect(diags.HasError()).To(BeFalse())
			m.Patches = list
			return m
		}

		planModelWithPatchesAndOnConflict := func(patches []string, onConflict string) branchModel {
			m := planModelWithPatches(patches)
			m.OnConflict = types.StringValue(onConflict)
			return m
		}

		// priorStateModel builds the resource's prior state, i.e. what Update
		// reads via req.State to compare against the newly resolved base_sha
		// for the fast-forward/rewrite check. base == "" means no prior
		// observation (skips the check, like the model right after Create).
		priorStateModel := func(base string) branchModel {
			return branchModel{
				Id:          types.StringValue(repoURL + "#" + refName),
				Repository:  branchRepoModel{Url: types.StringValue(repoURL), Host: types.StringNull(), Auth: nil},
				Name:        types.StringValue(refName),
				BaseRef:     types.StringValue(refName),
				BaseSha:     types.StringValue(base),
				ResolvedRef: types.StringValue(base),
				Patches:     types.ListNull(types.StringType),
				OnConflict:  types.StringValue("force"),
			}
		}

		// priorStateModelWithResolvedRef is like priorStateModel, but lets
		// resolved_ref be set independently of base_sha, for on_conflict =
		// "fail" tests where resolved_ref is what Update passes to
		// ApplyPatches as the compare-and-swap ExpectedTip.
		priorStateModelWithResolvedRef := func(base, resolvedRef string, patches []string) branchModel {
			m := priorStateModel(base)
			m.ResolvedRef = types.StringValue(resolvedRef)
			if patches != nil {
				list, diags := types.ListValueFrom(context.Background(), types.StringType, patches)
				Expect(diags.HasError()).To(BeFalse())
				m.Patches = list
			}
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

				req := resource.UpdateRequest{
					Plan:  buildBranchPlan(s, planModel()),
					State: buildBranchState(s, priorStateModel(hash)),
				}
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

				req := resource.UpdateRequest{
					Plan:  buildBranchPlan(s, planModel()),
					State: buildBranchState(s, priorStateModel(hash)),
				}
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

				req := resource.UpdateRequest{
					Plan:  buildBranchPlan(s, planModelWithPatches(patches)),
					State: buildBranchState(s, priorStateModel(hash)),
				}
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

				req := resource.UpdateRequest{
					Plan:  buildBranchPlan(s, planModelWithPatches(patches)),
					State: buildBranchState(s, priorStateModel(hash)),
				}
				resp := &resource.UpdateResponse{State: tfsdk.State{Schema: s}}

				Expect(func() {
					branchR.Update(context.Background(), req, resp)
				}).NotTo(Panic())

				Expect(resp.Diagnostics.HasError()).To(BeTrue())
			})
		})

		Context("when on_conflict is \"force\" (the default) and patches are set", func() {
			It("does not set ExpectedTip on the ApplyPatches request", func() {
				patches := []string{"diff --git a/x b/x"}
				var gotReq git.ApplyPatchesRequest
				fake := &fakeGitClient{
					lsRemoteFunc: func(ctx context.Context, url string, auth git.Auth) ([]git.Ref, error) {
						return []git.Ref{{Name: "refs/heads/main", Hash: hash}}, nil
					},
					applyPatchesFunc: func(ctx context.Context, req git.ApplyPatchesRequest) (git.ApplyPatchesResult, error) {
						gotReq = req
						return git.ApplyPatchesResult{ResolvedSHA: hash}, nil
					},
				}
				branchR := newBranchResourceWithClient(fake)
				s := branchResourceSchema(branchR)

				req := resource.UpdateRequest{
					Plan:  buildBranchPlan(s, planModelWithPatchesAndOnConflict(patches, "force")),
					State: buildBranchState(s, priorStateModelWithResolvedRef(hash, "some-prior-tip", patches)),
				}
				resp := &resource.UpdateResponse{State: tfsdk.State{Schema: s}}

				branchR.Update(context.Background(), req, resp)

				Expect(resp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", resp.Diagnostics))
				Expect(gotReq.ExpectedTip).To(BeEmpty())
			})
		})

		Context("when on_conflict is \"fail\" and patches are set", func() {
			It("passes the prior resolved_ref as ExpectedTip on the ApplyPatches request", func() {
				patches := []string{"diff --git a/x b/x"}
				const priorTip = "5555555555555555555555555555555555555f"
				var gotReq git.ApplyPatchesRequest
				fake := &fakeGitClient{
					lsRemoteFunc: func(ctx context.Context, url string, auth git.Auth) ([]git.Ref, error) {
						return []git.Ref{{Name: "refs/heads/main", Hash: hash}}, nil
					},
					applyPatchesFunc: func(ctx context.Context, req git.ApplyPatchesRequest) (git.ApplyPatchesResult, error) {
						gotReq = req
						return git.ApplyPatchesResult{ResolvedSHA: hash}, nil
					},
				}
				branchR := newBranchResourceWithClient(fake)
				s := branchResourceSchema(branchR)

				req := resource.UpdateRequest{
					Plan:  buildBranchPlan(s, planModelWithPatchesAndOnConflict(patches, "fail")),
					State: buildBranchState(s, priorStateModelWithResolvedRef(hash, priorTip, patches)),
				}
				resp := &resource.UpdateResponse{State: tfsdk.State{Schema: s}}

				branchR.Update(context.Background(), req, resp)

				Expect(resp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", resp.Diagnostics))
				Expect(gotReq.ExpectedTip).To(Equal(priorTip))
			})

			It("adds a distinct conflict diagnostic without panicking when ApplyPatches returns a ConflictError", func() {
				patches := []string{"diff --git a/x b/x"}
				const priorTip = "5555555555555555555555555555555555555f"
				fake := &fakeGitClient{
					lsRemoteFunc: func(ctx context.Context, url string, auth git.Auth) ([]git.Ref, error) {
						return []git.Ref{{Name: "refs/heads/main", Hash: hash}}, nil
					},
					applyPatchesFunc: func(ctx context.Context, req git.ApplyPatchesRequest) (git.ApplyPatchesResult, error) {
						return git.ApplyPatchesResult{}, &git.ConflictError{
							Branch:      refName,
							ExpectedTip: req.ExpectedTip,
							Err:         fmt.Errorf("stale info"),
						}
					},
				}
				branchR := newBranchResourceWithClient(fake)
				s := branchResourceSchema(branchR)

				req := resource.UpdateRequest{
					Plan:  buildBranchPlan(s, planModelWithPatchesAndOnConflict(patches, "fail")),
					State: buildBranchState(s, priorStateModelWithResolvedRef(hash, priorTip, patches)),
				}
				resp := &resource.UpdateResponse{State: tfsdk.State{Schema: s}}

				Expect(func() {
					branchR.Update(context.Background(), req, resp)
				}).NotTo(Panic())

				Expect(resp.Diagnostics.HasError()).To(BeTrue())
				Expect(resp.Diagnostics.Errors()[0].Summary()).To(Equal("Conflict Detected: Branch Tip Changed Since Last Read"))
			})
		})

		Context("when the base ref resolves to a hash that fast-forwards the prior base_sha", func() {
			It("does not add a warning diagnostic", func() {
				const oldHash = "3333333333333333333333333333333333333d"
				fake := &fakeGitClient{
					lsRemoteFunc: func(ctx context.Context, url string, auth git.Auth) ([]git.Ref, error) {
						return []git.Ref{{Name: "refs/heads/main", Hash: hash}}, nil
					},
					isAncestorFunc: func(ctx context.Context, url string, auth git.Auth, ancestor, descendant string) (bool, error) {
						Expect(ancestor).To(Equal(oldHash))
						Expect(descendant).To(Equal(hash))
						return true, nil
					},
				}
				branchR := newBranchResourceWithClient(fake)
				s := branchResourceSchema(branchR)

				req := resource.UpdateRequest{
					Plan:  buildBranchPlan(s, planModel()),
					State: buildBranchState(s, priorStateModel(oldHash)),
				}
				resp := &resource.UpdateResponse{State: tfsdk.State{Schema: s}}

				branchR.Update(context.Background(), req, resp)

				Expect(resp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", resp.Diagnostics))
				Expect(resp.Diagnostics.WarningsCount()).To(Equal(0))
			})
		})

		Context("when the base ref resolves to a hash that is not a fast-forward of the prior base_sha", func() {
			It("adds a warning diagnostic but still writes state", func() {
				const oldHash = "3333333333333333333333333333333333333d"
				fake := &fakeGitClient{
					lsRemoteFunc: func(ctx context.Context, url string, auth git.Auth) ([]git.Ref, error) {
						return []git.Ref{{Name: "refs/heads/main", Hash: hash}}, nil
					},
					isAncestorFunc: func(ctx context.Context, url string, auth git.Auth, ancestor, descendant string) (bool, error) {
						return false, nil
					},
				}
				branchR := newBranchResourceWithClient(fake)
				s := branchResourceSchema(branchR)

				req := resource.UpdateRequest{
					Plan:  buildBranchPlan(s, planModel()),
					State: buildBranchState(s, priorStateModel(oldHash)),
				}
				resp := &resource.UpdateResponse{State: tfsdk.State{Schema: s}}

				branchR.Update(context.Background(), req, resp)

				Expect(resp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", resp.Diagnostics))
				Expect(resp.Diagnostics.WarningsCount()).To(Equal(1))

				var got branchModel
				Expect(resp.State.Get(context.Background(), &got).HasError()).To(BeFalse())
				Expect(got.BaseSha.ValueString()).To(Equal(hash))
			})
		})

		Context("when there is no prior base_sha (e.g. right after Create)", func() {
			It("never calls IsAncestor", func() {
				fake := &fakeGitClient{
					lsRemoteFunc: func(ctx context.Context, url string, auth git.Auth) ([]git.Ref, error) {
						return []git.Ref{{Name: "refs/heads/main", Hash: hash}}, nil
					},
					// isAncestorFunc intentionally left nil: if Update calls
					// IsAncestor it will panic and fail this test.
				}
				branchR := newBranchResourceWithClient(fake)
				s := branchResourceSchema(branchR)

				req := resource.UpdateRequest{
					Plan:  buildBranchPlan(s, planModel()),
					State: buildBranchState(s, priorStateModel("")),
				}
				resp := &resource.UpdateResponse{State: tfsdk.State{Schema: s}}

				Expect(func() {
					branchR.Update(context.Background(), req, resp)
				}).NotTo(Panic())

				Expect(resp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", resp.Diagnostics))
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
				OnConflict:  types.StringValue("force"),
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
				Expect(got.OnConflict.ValueString()).To(Equal("force"))

				Expect(fake.gotAuth).To(Equal(git.Auth{}))
			})

			It("splits on the last # in the import ID", func() {
				// URLs may legitimately contain a "#" (e.g. a URL
				// fragment), while branch names generally do not. Splitting
				// on the last "#" (rather than the first) is what lets a
				// url like this round-trip correctly.
				const urlWithHash = "https://example.com/repo.git#fragment"
				fake := &fakeGitClient{
					lsRemoteFunc: func(ctx context.Context, url string, auth git.Auth) ([]git.Ref, error) {
						Expect(url).To(Equal(urlWithHash))
						return []git.Ref{{Name: "refs/heads/" + refName, Hash: hash}}, nil
					},
				}
				branchR := newBranchResourceWithClient(fake)
				s := branchResourceSchema(branchR)
				importer, ok := branchR.(resource.ResourceWithImportState)
				Expect(ok).To(BeTrue())

				importID := urlWithHash + "#" + refName
				Expect(strings.Count(importID, "#")).To(BeNumerically(">", 1))

				req := resource.ImportStateRequest{ID: importID}
				resp := &resource.ImportStateResponse{State: tfsdk.State{Schema: s}}

				importer.ImportState(context.Background(), req, resp)

				Expect(resp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", resp.Diagnostics))

				var got branchModel
				Expect(resp.State.Get(context.Background(), &got).HasError()).To(BeFalse())
				Expect(got.Repository.Url.ValueString()).To(Equal(urlWithHash))
				Expect(got.Name.ValueString()).To(Equal(refName))
			})
		})
	})
})

func TestAccGitBranch_basic(t *testing.T) {
	repoDir := newTestRepo(t)
	repoURL := "file://" + repoDir

	// The default branch name depends on the host's git config, so ask the repo rather than hardcoding it.
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

// TestAccGitBranch_patches exercises the patches attribute end to end,
// pinning the exec backend to keep it covered by an acceptance test
// alongside gogit's own unit tests in internal/git/gogit.
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
