package provider

// This file's specs cover the github-source path of git_patch's Create,
// which needs a fake github.Client injected into the unexported
// gitPatchResource.github field. That field, and the gitPatchResource type
// itself, are unexported, so this file intentionally lives in package
// provider (not provider_test) to reach them directly, following the
// pattern the task briefing asked for
// (`&gitPatchResource{github: &fakeGitHubClient{...}}`).
//
// The fakeGitHubClient double used by the rest of the suite lives in
// testutil_test.go under package provider_test, which this package cannot
// import (provider_test already imports provider, so the reverse would be
// a cycle) and could not use anyway since it's unexported. So this file
// defines its own minimal fake satisfying github.Client. See the final
// report for a note recommending a proper test seam (e.g. a functional
// option on NewGitPatchResource) so github-source specs like these can
// live alongside the rest of the git_patch_resource specs in
// provider_test instead of needing this internal-package split.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/UnstoppableMango/terraform-provider-git/internal/git/github"
)

type internalFakeGitHubClient struct {
	resolvePRFunc     func(ctx context.Context, repository string, pr int64, auth github.Auth) (github.Resolution, error)
	resolveCommitFunc func(ctx context.Context, repository string, sha string, auth github.Auth) (github.Resolution, error)

	gotRepository string
	gotPr         int64
	gotCommit     string
	gotAuth       github.Auth
}

var _ github.Client = (*internalFakeGitHubClient)(nil)

func (f *internalFakeGitHubClient) ResolvePR(ctx context.Context, repository string, pr int64, auth github.Auth) (github.Resolution, error) {
	f.gotRepository = repository
	f.gotPr = pr
	f.gotAuth = auth
	return f.resolvePRFunc(ctx, repository, pr, auth)
}

func (f *internalFakeGitHubClient) ResolveCommit(ctx context.Context, repository string, sha string, auth github.Auth) (github.Resolution, error) {
	f.gotRepository = repository
	f.gotCommit = sha
	f.gotAuth = auth
	return f.resolveCommitFunc(ctx, repository, sha, auth)
}

func sha256HexInternal(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

var _ = Describe("gitPatchResource github source", func() {
	var patchSchema resource.SchemaResponse

	BeforeEach(func() {
		patchSchema = resource.SchemaResponse{}
		(&gitPatchResource{}).Schema(context.Background(), resource.SchemaRequest{}, &patchSchema)
	})

	newCreateRequest := func(model gitPatchResourceModel) (resource.CreateRequest, *resource.CreateResponse) {
		ctx := context.Background()

		plan := tfsdk.Plan{Schema: patchSchema.Schema}
		Expect(plan.Set(ctx, &model).HasError()).To(BeFalse())

		req := resource.CreateRequest{
			Config: tfsdk.Config{Schema: patchSchema.Schema, Raw: plan.Raw},
			Plan:   plan,
		}
		resp := &resource.CreateResponse{State: tfsdk.State{Schema: patchSchema.Schema}}

		return req, resp
	}

	Context("with github.pr set", func() {
		It("resolves diff, id, and github.sha from ResolvePR, passing through repository/pr/auth", func() {
			const (
				repository   = "acme/widgets"
				prNumber     = int64(7)
				resolvedSHA  = "cafef00d"
				resolvedDiff = "diff --git a/pr b/pr\n+from-pr\n"
			)

			fake := &internalFakeGitHubClient{
				resolvePRFunc: func(ctx context.Context, repository string, pr int64, auth github.Auth) (github.Resolution, error) {
					return github.Resolution{SHA: resolvedSHA, Diff: resolvedDiff}, nil
				},
			}
			r := &gitPatchResource{github: fake}

			model := gitPatchResourceModel{
				Id:      types.StringUnknown(),
				Content: types.StringNull(),
				File:    types.StringNull(),
				Diff:    types.StringUnknown(),
				Github: &gitPatchGithubModel{
					Repository: types.StringValue(repository),
					Pr:         types.Int64Value(prNumber),
					Commit:     types.StringNull(),
					Sha:        types.StringUnknown(),
				},
				Auth: &gitRepositoryAuthModel{
					Token: types.StringValue("tok-123"),
				},
			}
			req, resp := newCreateRequest(model)

			r.Create(context.Background(), req, resp)

			Expect(resp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", resp.Diagnostics))

			var got gitPatchResourceModel
			Expect(resp.State.Get(context.Background(), &got).HasError()).To(BeFalse())

			Expect(got.Diff.ValueString()).To(Equal(resolvedDiff))
			Expect(got.Id.ValueString()).To(Equal(sha256HexInternal(resolvedDiff)))
			Expect(got.Github.Sha.ValueString()).To(Equal(resolvedSHA))

			Expect(fake.gotRepository).To(Equal(repository))
			Expect(fake.gotPr).To(Equal(prNumber))
			Expect(fake.gotAuth).To(Equal(github.Auth{Token: "tok-123"}))
		})
	})

	Context("with github.commit set", func() {
		It("resolves diff, id, and github.sha from ResolveCommit, passing through repository/commit/auth", func() {
			const (
				repository   = "acme/widgets"
				commitSHA    = "deadbeef"
				resolvedDiff = "diff --git a/commit b/commit\n+from-commit\n"
			)

			fake := &internalFakeGitHubClient{
				resolveCommitFunc: func(ctx context.Context, repository string, sha string, auth github.Auth) (github.Resolution, error) {
					return github.Resolution{SHA: sha, Diff: resolvedDiff}, nil
				},
			}
			r := &gitPatchResource{github: fake}

			model := gitPatchResourceModel{
				Id:      types.StringUnknown(),
				Content: types.StringNull(),
				File:    types.StringNull(),
				Diff:    types.StringUnknown(),
				Github: &gitPatchGithubModel{
					Repository: types.StringValue(repository),
					Pr:         types.Int64Null(),
					Commit:     types.StringValue(commitSHA),
					Sha:        types.StringUnknown(),
				},
				Auth: nil,
			}
			req, resp := newCreateRequest(model)

			r.Create(context.Background(), req, resp)

			Expect(resp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", resp.Diagnostics))

			var got gitPatchResourceModel
			Expect(resp.State.Get(context.Background(), &got).HasError()).To(BeFalse())

			Expect(got.Diff.ValueString()).To(Equal(resolvedDiff))
			Expect(got.Id.ValueString()).To(Equal(sha256HexInternal(resolvedDiff)))
			Expect(got.Github.Sha.ValueString()).To(Equal(commitSHA))

			Expect(fake.gotRepository).To(Equal(repository))
			Expect(fake.gotCommit).To(Equal(commitSHA))
			Expect(fake.gotAuth).To(Equal(github.Auth{}))
		})
	})

	Context("determinism across sources", func() {
		It("produces the same id for a github-resolved diff as for the same content given directly", func() {
			const sharedDiff = "diff --git a/shared b/shared\n+identical\n"

			fake := &internalFakeGitHubClient{
				resolveCommitFunc: func(ctx context.Context, repository string, sha string, auth github.Auth) (github.Resolution, error) {
					return github.Resolution{SHA: sha, Diff: sharedDiff}, nil
				},
			}
			githubR := &gitPatchResource{github: fake}
			contentR := &gitPatchResource{}

			githubReq, githubResp := newCreateRequest(gitPatchResourceModel{
				Id:   types.StringUnknown(),
				Diff: types.StringUnknown(),
				Github: &gitPatchGithubModel{
					Repository: types.StringValue("acme/widgets"),
					Pr:         types.Int64Null(),
					Commit:     types.StringValue("somesha"),
					Sha:        types.StringUnknown(),
				},
			})
			githubR.Create(context.Background(), githubReq, githubResp)
			Expect(githubResp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", githubResp.Diagnostics))

			contentReq, contentResp := newCreateRequest(gitPatchResourceModel{
				Id:      types.StringUnknown(),
				Content: types.StringValue(sharedDiff),
				Diff:    types.StringUnknown(),
			})
			contentR.Create(context.Background(), contentReq, contentResp)
			Expect(contentResp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", contentResp.Diagnostics))

			var gotFromGithub, gotFromContent gitPatchResourceModel
			Expect(githubResp.State.Get(context.Background(), &gotFromGithub).HasError()).To(BeFalse())
			Expect(contentResp.State.Get(context.Background(), &gotFromContent).HasError()).To(BeFalse())

			Expect(gotFromGithub.Id.ValueString()).To(Equal(gotFromContent.Id.ValueString()))
		})
	})
})
