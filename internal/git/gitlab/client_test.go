package gitlab_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/UnstoppableMango/terraform-provider-git/internal/git/gitlab"
)

func TestGitlab(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "internal/git/gitlab Suite")
}

// The fake server below stands in for the real GitLab REST API. Unlike
// GitHub, GitLab has no endpoint that returns a ready-to-use unified diff:
// merge request/commit diffs come back as a JSON array of per-file objects
// whose "diff" field holds only the "@@ ...@@" hunk body, so the client
// reconstructs the "diff --git"/"---"/"+++" header lines itself. See
// client.go's renderUnifiedDiff.
//
//   - ResolveMR issues GET /api/v4/projects/{project}/merge_requests/{iid}
//     (JSON, parsed for {"sha":"..."}) and paginated GET
//     .../merge_requests/{iid}/diffs (JSON array of per-file diff objects,
//     following the "X-Next-Page" response header until it's empty).
//   - ResolveCommit issues paginated GET
//     /api/v4/projects/{project}/repository/commits/{sha}/diff the same way,
//     returning the input sha unchanged as Resolution.SHA.
//   - Requests carry a "Private-Token: <token>" header when a token is
//     provided (GitLab's PAT convention, unlike GitHub's Bearer scheme).
//   - A non-2xx response becomes a non-nil error whose message includes the
//     response status code.
//
// The client is constructed via gitlab.New, pointed at the fake server via a
// gitlab.WithBaseURL(baseURL) option.
var _ = Describe("Client", func() {
	const (
		project   = "group/project"
		mrIID     = int64(42)
		headSHA   = "abc123def456"
		commitSHA = "deadbeefcafe0"
	)

	mrPath := fmt.Sprintf("/api/v4/projects/%s/merge_requests/%d", project, mrIID)
	mrDiffsPath := mrPath + "/diffs"
	commitDiffPath := fmt.Sprintf("/api/v4/projects/%s/repository/commits/%s/diff", project, commitSHA)

	Describe("ResolveMR", func() {
		It("resolves the head sha and synthesizes a unified diff from the merge request's changed files", func() {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case mrPath:
					Expect(r.Method).To(Equal(http.MethodGet))
					w.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprintf(w, `{"sha":%q}`, headSHA)
				case mrDiffsPath:
					Expect(r.Method).To(Equal(http.MethodGet))
					w.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprint(w, `[
						{"old_path":"modified.txt","new_path":"modified.txt","a_mode":"100644","b_mode":"100644","diff":"@@ -1 +1 @@\n-old\n+bar\n"},
						{"old_path":"","new_path":"added.txt","b_mode":"100644","new_file":true,"diff":"@@ -0,0 +1 @@\n+hello\n"},
						{"old_path":"removed.txt","new_path":"","a_mode":"100644","deleted_file":true,"diff":"@@ -1 +0,0 @@\n-bye\n"}
					]`)
				default:
					w.WriteHeader(http.StatusNotFound)
					_, _ = fmt.Fprintf(w, "unexpected path: %s", r.URL.Path)
				}
			}))
			defer server.Close()

			client := gitlab.New(gitlab.WithBaseURL(server.URL), gitlab.WithoutRetries())

			resolution, err := client.ResolveMR(context.Background(), project, mrIID, "")

			Expect(err).NotTo(HaveOccurred())
			Expect(resolution.SHA).To(Equal(headSHA))
			Expect(resolution.Diff).To(Equal(strings.Join([]string{
				"diff --git a/modified.txt b/modified.txt",
				"--- a/modified.txt",
				"+++ b/modified.txt",
				"@@ -1 +1 @@",
				"-old",
				"+bar",
				"diff --git a/added.txt b/added.txt",
				"new file mode 100644",
				"--- /dev/null",
				"+++ b/added.txt",
				"@@ -0,0 +1 @@",
				"+hello",
				"diff --git a/removed.txt b/removed.txt",
				"deleted file mode 100644",
				"--- a/removed.txt",
				"+++ /dev/null",
				"@@ -1 +0,0 @@",
				"-bye",
				"",
			}, "\n")))

			// The real consumer of this string is a unified diff parser, not
			// a human, so also assert it actually parses correctly.
			files, _, err := gitdiff.Parse(strings.NewReader(resolution.Diff))
			Expect(err).NotTo(HaveOccurred())
			Expect(files).To(HaveLen(3))
			Expect(files[0].NewName).To(Equal("modified.txt"))
			Expect(files[1].IsNew).To(BeTrue())
			Expect(files[1].NewName).To(Equal("added.txt"))
			Expect(files[2].IsDelete).To(BeTrue())
			Expect(files[2].OldName).To(Equal("removed.txt"))
		})

		It("follows pagination across multiple pages of diffs", func() {
			var gotPages []string

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case mrPath:
					_, _ = fmt.Fprintf(w, `{"sha":%q}`, headSHA)
				case mrDiffsPath:
					page := r.URL.Query().Get("page")
					gotPages = append(gotPages, page)
					if page == "" || page == "1" {
						w.Header().Set("X-Next-Page", "2")
						_, _ = fmt.Fprint(w, `[{"old_path":"a.txt","new_path":"a.txt","diff":"@@ -1 +1 @@\n-1\n+2\n"}]`)
						return
					}
					_, _ = fmt.Fprint(w, `[{"old_path":"b.txt","new_path":"b.txt","diff":"@@ -1 +1 @@\n-3\n+4\n"}]`)
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()

			client := gitlab.New(gitlab.WithBaseURL(server.URL), gitlab.WithoutRetries())

			resolution, err := client.ResolveMR(context.Background(), project, mrIID, "")

			Expect(err).NotTo(HaveOccurred())
			Expect(gotPages).To(Equal([]string{"", "2"}))
			Expect(resolution.Diff).To(ContainSubstring("diff --git a/a.txt b/a.txt"))
			Expect(resolution.Diff).To(ContainSubstring("diff --git a/b.txt b/b.txt"))
			Expect(strings.Index(resolution.Diff, "a.txt")).To(BeNumerically("<", strings.Index(resolution.Diff, "b.txt")))
		})

		It("sends a Private-Token header on both requests when a token is set", func() {
			var gotMRAuth, gotDiffsAuth string

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case mrPath:
					gotMRAuth = r.Header.Get("Private-Token")
					_, _ = fmt.Fprintf(w, `{"sha":%q}`, headSHA)
				case mrDiffsPath:
					gotDiffsAuth = r.Header.Get("Private-Token")
					_, _ = fmt.Fprint(w, `[]`)
				}
			}))
			defer server.Close()

			client := gitlab.New(gitlab.WithBaseURL(server.URL), gitlab.WithoutRetries())

			_, err := client.ResolveMR(context.Background(), project, mrIID, "s3cr3t")

			Expect(err).NotTo(HaveOccurred())
			Expect(gotMRAuth).To(Equal("s3cr3t"))
			Expect(gotDiffsAuth).To(Equal("s3cr3t"))
		})

		It("omits the Private-Token header when no token is set", func() {
			sawAuthHeader := false

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Private-Token") != "" {
					sawAuthHeader = true
				}
				switch r.URL.Path {
				case mrPath:
					_, _ = fmt.Fprintf(w, `{"sha":%q}`, headSHA)
				case mrDiffsPath:
					_, _ = fmt.Fprint(w, `[]`)
				}
			}))
			defer server.Close()

			client := gitlab.New(gitlab.WithBaseURL(server.URL), gitlab.WithoutRetries())

			_, err := client.ResolveMR(context.Background(), project, mrIID, "")

			Expect(err).NotTo(HaveOccurred())
			Expect(sawAuthHeader).To(BeFalse())
		})

		It("returns an error including the status code for a non-2xx response resolving the merge request", func() {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				_, _ = fmt.Fprint(w, `{"message":"404 Not found"}`)
			}))
			defer server.Close()

			client := gitlab.New(gitlab.WithBaseURL(server.URL), gitlab.WithoutRetries())

			_, err := client.ResolveMR(context.Background(), project, mrIID, "")

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("404"))
		})

		It("returns an error including the status code for a non-2xx response fetching diffs", func() {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case mrPath:
					_, _ = fmt.Fprintf(w, `{"sha":%q}`, headSHA)
				case mrDiffsPath:
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = fmt.Fprint(w, "boom")
				}
			}))
			defer server.Close()

			client := gitlab.New(gitlab.WithBaseURL(server.URL), gitlab.WithoutRetries())

			_, err := client.ResolveMR(context.Background(), project, mrIID, "")

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("500"))
		})
	})

	Describe("ResolveCommit", func() {
		It("synthesizes a unified diff from the commit's changed files and returns the sha unchanged", func() {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.URL.Path).To(Equal(commitDiffPath))
				Expect(r.Method).To(Equal(http.MethodGet))
				_, _ = fmt.Fprint(w, `[{"old_path":"foo","new_path":"foo","diff":"@@ -1 +1 @@\n-old\n+bar\n"}]`)
			}))
			defer server.Close()

			client := gitlab.New(gitlab.WithBaseURL(server.URL), gitlab.WithoutRetries())

			resolution, err := client.ResolveCommit(context.Background(), project, commitSHA, "")

			Expect(err).NotTo(HaveOccurred())
			Expect(resolution.SHA).To(Equal(commitSHA))
			Expect(resolution.Diff).To(ContainSubstring("diff --git a/foo b/foo"))

			files, _, err := gitdiff.Parse(strings.NewReader(resolution.Diff))
			Expect(err).NotTo(HaveOccurred())
			Expect(files).To(HaveLen(1))
			Expect(files[0].NewName).To(Equal("foo"))
		})

		It("sends a Private-Token header when a token is set", func() {
			var gotAuth string

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotAuth = r.Header.Get("Private-Token")
				_, _ = fmt.Fprint(w, `[]`)
			}))
			defer server.Close()

			client := gitlab.New(gitlab.WithBaseURL(server.URL), gitlab.WithoutRetries())

			_, err := client.ResolveCommit(context.Background(), project, commitSHA, "s3cr3t")

			Expect(err).NotTo(HaveOccurred())
			Expect(gotAuth).To(Equal("s3cr3t"))
		})

		It("omits the Private-Token header when no token is set", func() {
			sawAuthHeader := false

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sawAuthHeader = r.Header.Get("Private-Token") != ""
				_, _ = fmt.Fprint(w, `[]`)
			}))
			defer server.Close()

			client := gitlab.New(gitlab.WithBaseURL(server.URL), gitlab.WithoutRetries())

			_, err := client.ResolveCommit(context.Background(), project, commitSHA, "")

			Expect(err).NotTo(HaveOccurred())
			Expect(sawAuthHeader).To(BeFalse())
		})

		It("returns an error including the status code for a non-2xx response", func() {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				_, _ = fmt.Fprint(w, `{"message":"404 Not found"}`)
			}))
			defer server.Close()

			client := gitlab.New(gitlab.WithBaseURL(server.URL), gitlab.WithoutRetries())

			_, err := client.ResolveCommit(context.Background(), project, commitSHA, "")

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("404"))
		})
	})
})
