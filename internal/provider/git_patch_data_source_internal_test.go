package provider

// This file's specs cover the github- and gitlab-source paths of git_patch's
// Read, which need fake github.Client/gitlab.Client instances injected into
// the unexported gitPatchDataSource.github/gitlab fields. Those fields, and
// the gitPatchDataSource type itself, are unexported, so this file
// intentionally lives in package provider (not provider_test) to reach them
// directly.
//
// The fakeGitHubClient double used by the rest of the suite lives in
// testutil_test.go under package provider_test, which this package cannot
// import (provider_test already imports provider, so the reverse would be
// a cycle) and could not use anyway since it's unexported. So this file
// defines its own minimal fakes satisfying github.Client/gitlab.Client.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/UnstoppableMango/terraform-provider-git/internal/git/github"
	"github.com/UnstoppableMango/terraform-provider-git/internal/git/gitlab"
)

type internalFakeGitHubClient struct {
	resolvePRFunc     func(ctx context.Context, repository string, pr int64, token string) (github.Resolution, error)
	resolveCommitFunc func(ctx context.Context, repository, sha, token string) (github.Resolution, error)

	gotRepository string
	gotPr         int64
	gotCommit     string
	gotToken      string
}

var _ github.Client = (*internalFakeGitHubClient)(nil)

func (f *internalFakeGitHubClient) ResolvePR(ctx context.Context, repository string, pr int64, token string) (github.Resolution, error) {
	f.gotRepository = repository
	f.gotPr = pr
	f.gotToken = token
	return f.resolvePRFunc(ctx, repository, pr, token)
}

func (f *internalFakeGitHubClient) ResolveCommit(ctx context.Context, repository, sha, token string) (github.Resolution, error) {
	f.gotRepository = repository
	f.gotCommit = sha
	f.gotToken = token
	return f.resolveCommitFunc(ctx, repository, sha, token)
}

type internalFakeGitlabClient struct {
	resolveMRFunc     func(ctx context.Context, project string, mr int64, token string) (gitlab.Resolution, error)
	resolveCommitFunc func(ctx context.Context, project, sha, token string) (gitlab.Resolution, error)

	gotProject string
	gotMr      int64
	gotCommit  string
	gotToken   string
}

var _ gitlab.Client = (*internalFakeGitlabClient)(nil)

func (f *internalFakeGitlabClient) ResolveMR(ctx context.Context, project string, mr int64, token string) (gitlab.Resolution, error) {
	f.gotProject = project
	f.gotMr = mr
	f.gotToken = token
	return f.resolveMRFunc(ctx, project, mr, token)
}

func (f *internalFakeGitlabClient) ResolveCommit(ctx context.Context, project, sha, token string) (gitlab.Resolution, error) {
	f.gotProject = project
	f.gotCommit = sha
	f.gotToken = token
	return f.resolveCommitFunc(ctx, project, sha, token)
}

func sha256HexInternal(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

var _ = Describe("gitPatchDataSource github source", func() {
	var patchSchema datasource.SchemaResponse

	BeforeEach(func() {
		patchSchema = datasource.SchemaResponse{}
		(&gitPatchDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &patchSchema)
	})

	newReadRequest := func(model gitPatchResourceModel) (datasource.ReadRequest, *datasource.ReadResponse) {
		ctx := context.Background()

		built := tfsdk.State{Schema: patchSchema.Schema}
		Expect(built.Set(ctx, &model).HasError()).To(BeFalse())

		req := datasource.ReadRequest{
			Config: tfsdk.Config{Schema: patchSchema.Schema, Raw: built.Raw},
		}
		resp := &datasource.ReadResponse{State: tfsdk.State{Schema: patchSchema.Schema}}

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
				resolvePRFunc: func(ctx context.Context, repository string, pr int64, token string) (github.Resolution, error) {
					return github.Resolution{SHA: resolvedSHA, Diff: resolvedDiff}, nil
				},
			}
			ds := &gitPatchDataSource{github: fake}

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
				Auth: &gitPatchAuthModel{
					Token: types.StringValue("tok-123"),
				},
			}
			req, resp := newReadRequest(model)

			ds.Read(context.Background(), req, resp)

			Expect(resp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", resp.Diagnostics))

			var got gitPatchResourceModel
			Expect(resp.State.Get(context.Background(), &got).HasError()).To(BeFalse())

			Expect(got.Diff.ValueString()).To(Equal(resolvedDiff))
			Expect(got.Id.ValueString()).To(Equal(sha256HexInternal(resolvedDiff)))
			Expect(got.Github.Sha.ValueString()).To(Equal(resolvedSHA))

			Expect(fake.gotRepository).To(Equal(repository))
			Expect(fake.gotPr).To(Equal(prNumber))
			Expect(fake.gotToken).To(Equal("tok-123"))
		})
	})

	Context("with no per-resource auth but a provider default token", func() {
		It("passes the provider default token through to ResolveCommit", func() {
			const (
				repository   = "acme/widgets"
				commitSHA    = "deadbeef"
				resolvedDiff = "diff --git a/commit b/commit\n+from-commit\n"
			)

			fake := &internalFakeGitHubClient{
				resolveCommitFunc: func(ctx context.Context, repository string, sha string, token string) (github.Resolution, error) {
					return github.Resolution{SHA: sha, Diff: resolvedDiff}, nil
				},
			}
			ds := &gitPatchDataSource{github: fake, defaultToken: "provider-tok"}

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
			req, resp := newReadRequest(model)

			ds.Read(context.Background(), req, resp)

			Expect(resp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", resp.Diagnostics))
			Expect(fake.gotToken).To(Equal("provider-tok"))
		})

		It("prefers the per-resource token over the provider default", func() {
			const (
				repository   = "acme/widgets"
				commitSHA    = "deadbeef"
				resolvedDiff = "diff --git a/commit b/commit\n+from-commit\n"
			)

			fake := &internalFakeGitHubClient{
				resolveCommitFunc: func(ctx context.Context, repository string, sha string, token string) (github.Resolution, error) {
					return github.Resolution{SHA: sha, Diff: resolvedDiff}, nil
				},
			}
			ds := &gitPatchDataSource{github: fake, defaultToken: "provider-tok"}

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
				Auth: &gitPatchAuthModel{Token: types.StringValue("resource-tok")},
			}
			req, resp := newReadRequest(model)

			ds.Read(context.Background(), req, resp)

			Expect(resp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", resp.Diagnostics))
			Expect(fake.gotToken).To(Equal("resource-tok"))
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
				resolveCommitFunc: func(ctx context.Context, repository string, sha string, token string) (github.Resolution, error) {
					return github.Resolution{SHA: sha, Diff: resolvedDiff}, nil
				},
			}
			ds := &gitPatchDataSource{github: fake}

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
			req, resp := newReadRequest(model)

			ds.Read(context.Background(), req, resp)

			Expect(resp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", resp.Diagnostics))

			var got gitPatchResourceModel
			Expect(resp.State.Get(context.Background(), &got).HasError()).To(BeFalse())

			Expect(got.Diff.ValueString()).To(Equal(resolvedDiff))
			Expect(got.Id.ValueString()).To(Equal(sha256HexInternal(resolvedDiff)))
			Expect(got.Github.Sha.ValueString()).To(Equal(commitSHA))

			Expect(fake.gotRepository).To(Equal(repository))
			Expect(fake.gotCommit).To(Equal(commitSHA))
			Expect(fake.gotToken).To(Equal(""))
		})
	})

	Context("determinism across sources", func() {
		It("produces the same id for a github-resolved diff as for the same content given directly", func() {
			const sharedDiff = "diff --git a/shared b/shared\n+identical\n"

			fake := &internalFakeGitHubClient{
				resolveCommitFunc: func(ctx context.Context, repository string, sha string, token string) (github.Resolution, error) {
					return github.Resolution{SHA: sha, Diff: sharedDiff}, nil
				},
			}
			githubDS := &gitPatchDataSource{github: fake}
			contentDS := &gitPatchDataSource{}

			githubReq, githubResp := newReadRequest(gitPatchResourceModel{
				Id:   types.StringUnknown(),
				Diff: types.StringUnknown(),
				Github: &gitPatchGithubModel{
					Repository: types.StringValue("acme/widgets"),
					Pr:         types.Int64Null(),
					Commit:     types.StringValue("somesha"),
					Sha:        types.StringUnknown(),
				},
			})
			githubDS.Read(context.Background(), githubReq, githubResp)
			Expect(githubResp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", githubResp.Diagnostics))

			contentReq, contentResp := newReadRequest(gitPatchResourceModel{
				Id:      types.StringUnknown(),
				Content: types.StringValue(sharedDiff),
				Diff:    types.StringUnknown(),
			})
			contentDS.Read(context.Background(), contentReq, contentResp)
			Expect(contentResp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", contentResp.Diagnostics))

			var gotFromGithub, gotFromContent gitPatchResourceModel
			Expect(githubResp.State.Get(context.Background(), &gotFromGithub).HasError()).To(BeFalse())
			Expect(contentResp.State.Get(context.Background(), &gotFromContent).HasError()).To(BeFalse())

			Expect(gotFromGithub.Id.ValueString()).To(Equal(gotFromContent.Id.ValueString()))
		})
	})
})

var _ = Describe("gitPatchDataSource gitlab source", func() {
	var patchSchema datasource.SchemaResponse

	BeforeEach(func() {
		patchSchema = datasource.SchemaResponse{}
		(&gitPatchDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &patchSchema)
	})

	newReadRequest := func(model gitPatchResourceModel) (datasource.ReadRequest, *datasource.ReadResponse) {
		ctx := context.Background()

		built := tfsdk.State{Schema: patchSchema.Schema}
		Expect(built.Set(ctx, &model).HasError()).To(BeFalse())

		req := datasource.ReadRequest{
			Config: tfsdk.Config{Schema: patchSchema.Schema, Raw: built.Raw},
		}
		resp := &datasource.ReadResponse{State: tfsdk.State{Schema: patchSchema.Schema}}

		return req, resp
	}

	Context("with gitlab.mr set", func() {
		It("resolves diff, id, and gitlab.sha from ResolveMR, passing through project/mr/auth", func() {
			const (
				project      = "acme/widgets"
				mrIID        = int64(7)
				resolvedSHA  = "cafef00d"
				resolvedDiff = "diff --git a/mr b/mr\n+from-mr\n"
			)

			fake := &internalFakeGitlabClient{
				resolveMRFunc: func(ctx context.Context, project string, mr int64, token string) (gitlab.Resolution, error) {
					return gitlab.Resolution{SHA: resolvedSHA, Diff: resolvedDiff}, nil
				},
			}
			ds := &gitPatchDataSource{gitlab: fake}

			model := gitPatchResourceModel{
				Id:      types.StringUnknown(),
				Content: types.StringNull(),
				File:    types.StringNull(),
				Diff:    types.StringUnknown(),
				Gitlab: &gitPatchGitlabModel{
					Project: types.StringValue(project),
					Mr:      types.Int64Value(mrIID),
					Commit:  types.StringNull(),
					Sha:     types.StringUnknown(),
				},
				Auth: &gitPatchAuthModel{
					Token: types.StringValue("tok-123"),
				},
			}
			req, resp := newReadRequest(model)

			ds.Read(context.Background(), req, resp)

			Expect(resp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", resp.Diagnostics))

			var got gitPatchResourceModel
			Expect(resp.State.Get(context.Background(), &got).HasError()).To(BeFalse())

			Expect(got.Diff.ValueString()).To(Equal(resolvedDiff))
			Expect(got.Id.ValueString()).To(Equal(sha256HexInternal(resolvedDiff)))
			Expect(got.Gitlab.Sha.ValueString()).To(Equal(resolvedSHA))

			Expect(fake.gotProject).To(Equal(project))
			Expect(fake.gotMr).To(Equal(mrIID))
			Expect(fake.gotToken).To(Equal("tok-123"))
		})
	})

	Context("with no per-resource auth but a provider default token", func() {
		It("passes the provider default token through to ResolveCommit", func() {
			const (
				project      = "acme/widgets"
				commitSHA    = "deadbeef"
				resolvedDiff = "diff --git a/commit b/commit\n+from-commit\n"
			)

			fake := &internalFakeGitlabClient{
				resolveCommitFunc: func(ctx context.Context, project string, sha string, token string) (gitlab.Resolution, error) {
					return gitlab.Resolution{SHA: sha, Diff: resolvedDiff}, nil
				},
			}
			ds := &gitPatchDataSource{gitlab: fake, defaultToken: "provider-tok"}

			model := gitPatchResourceModel{
				Id:      types.StringUnknown(),
				Content: types.StringNull(),
				File:    types.StringNull(),
				Diff:    types.StringUnknown(),
				Gitlab: &gitPatchGitlabModel{
					Project: types.StringValue(project),
					Mr:      types.Int64Null(),
					Commit:  types.StringValue(commitSHA),
					Sha:     types.StringUnknown(),
				},
				Auth: nil,
			}
			req, resp := newReadRequest(model)

			ds.Read(context.Background(), req, resp)

			Expect(resp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", resp.Diagnostics))
			Expect(fake.gotToken).To(Equal("provider-tok"))
		})

		It("prefers the per-resource token over the provider default", func() {
			const (
				project      = "acme/widgets"
				commitSHA    = "deadbeef"
				resolvedDiff = "diff --git a/commit b/commit\n+from-commit\n"
			)

			fake := &internalFakeGitlabClient{
				resolveCommitFunc: func(ctx context.Context, project string, sha string, token string) (gitlab.Resolution, error) {
					return gitlab.Resolution{SHA: sha, Diff: resolvedDiff}, nil
				},
			}
			ds := &gitPatchDataSource{gitlab: fake, defaultToken: "provider-tok"}

			model := gitPatchResourceModel{
				Id:      types.StringUnknown(),
				Content: types.StringNull(),
				File:    types.StringNull(),
				Diff:    types.StringUnknown(),
				Gitlab: &gitPatchGitlabModel{
					Project: types.StringValue(project),
					Mr:      types.Int64Null(),
					Commit:  types.StringValue(commitSHA),
					Sha:     types.StringUnknown(),
				},
				Auth: &gitPatchAuthModel{Token: types.StringValue("resource-tok")},
			}
			req, resp := newReadRequest(model)

			ds.Read(context.Background(), req, resp)

			Expect(resp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", resp.Diagnostics))
			Expect(fake.gotToken).To(Equal("resource-tok"))
		})
	})

	Context("with gitlab.commit set", func() {
		It("resolves diff, id, and gitlab.sha from ResolveCommit, passing through project/commit/auth", func() {
			const (
				project      = "acme/widgets"
				commitSHA    = "deadbeef"
				resolvedDiff = "diff --git a/commit b/commit\n+from-commit\n"
			)

			fake := &internalFakeGitlabClient{
				resolveCommitFunc: func(ctx context.Context, project string, sha string, token string) (gitlab.Resolution, error) {
					return gitlab.Resolution{SHA: sha, Diff: resolvedDiff}, nil
				},
			}
			ds := &gitPatchDataSource{gitlab: fake}

			model := gitPatchResourceModel{
				Id:      types.StringUnknown(),
				Content: types.StringNull(),
				File:    types.StringNull(),
				Diff:    types.StringUnknown(),
				Gitlab: &gitPatchGitlabModel{
					Project: types.StringValue(project),
					Mr:      types.Int64Null(),
					Commit:  types.StringValue(commitSHA),
					Sha:     types.StringUnknown(),
				},
				Auth: nil,
			}
			req, resp := newReadRequest(model)

			ds.Read(context.Background(), req, resp)

			Expect(resp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", resp.Diagnostics))

			var got gitPatchResourceModel
			Expect(resp.State.Get(context.Background(), &got).HasError()).To(BeFalse())

			Expect(got.Diff.ValueString()).To(Equal(resolvedDiff))
			Expect(got.Id.ValueString()).To(Equal(sha256HexInternal(resolvedDiff)))
			Expect(got.Gitlab.Sha.ValueString()).To(Equal(commitSHA))

			Expect(fake.gotProject).To(Equal(project))
			Expect(fake.gotCommit).To(Equal(commitSHA))
			Expect(fake.gotToken).To(Equal(""))
		})
	})

	Context("determinism across sources", func() {
		It("produces the same id for a gitlab-resolved diff as for the same content given directly", func() {
			const sharedDiff = "diff --git a/shared b/shared\n+identical\n"

			fake := &internalFakeGitlabClient{
				resolveCommitFunc: func(ctx context.Context, project string, sha string, token string) (gitlab.Resolution, error) {
					return gitlab.Resolution{SHA: sha, Diff: sharedDiff}, nil
				},
			}
			gitlabDS := &gitPatchDataSource{gitlab: fake}
			contentDS := &gitPatchDataSource{}

			gitlabReq, gitlabResp := newReadRequest(gitPatchResourceModel{
				Id:   types.StringUnknown(),
				Diff: types.StringUnknown(),
				Gitlab: &gitPatchGitlabModel{
					Project: types.StringValue("acme/widgets"),
					Mr:      types.Int64Null(),
					Commit:  types.StringValue("somesha"),
					Sha:     types.StringUnknown(),
				},
			})
			gitlabDS.Read(context.Background(), gitlabReq, gitlabResp)
			Expect(gitlabResp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", gitlabResp.Diagnostics))

			contentReq, contentResp := newReadRequest(gitPatchResourceModel{
				Id:      types.StringUnknown(),
				Content: types.StringValue(sharedDiff),
				Diff:    types.StringUnknown(),
			})
			contentDS.Read(context.Background(), contentReq, contentResp)
			Expect(contentResp.Diagnostics.HasError()).To(BeFalse(), fmt.Sprintf("%v", contentResp.Diagnostics))

			var gotFromGitlab, gotFromContent gitPatchResourceModel
			Expect(gitlabResp.State.Get(context.Background(), &gotFromGitlab).HasError()).To(BeFalse())
			Expect(contentResp.State.Get(context.Background(), &gotFromContent).HasError()).To(BeFalse())

			Expect(gotFromGitlab.Id.ValueString()).To(Equal(gotFromContent.Id.ValueString()))
		})
	})
})
