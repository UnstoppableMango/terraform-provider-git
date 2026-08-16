package github_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/UnstoppableMango/terraform-provider-git/internal/git/github"
)

func TestGithub(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "internal/git/github Suite")
}

// The fake server below stands in for the real GitHub REST API. The client is
// backed by go-github, so it speaks go-github's request contract:
//
//   - ResolvePR issues GET /repos/{owner}/{name}/pulls/{pr} twice: once with a
//     JSON Accept header (parsed for {"head":{"sha":"..."}} to obtain the head
//     commit sha), and once with an "application/vnd.github.v3.diff" Accept
//     header whose raw response body is the pull request diff.
//   - ResolveCommit issues GET /repos/{owner}/{name}/commits/{sha} with the
//     diff Accept header and uses the raw response body as the diff, returning
//     the input sha unchanged as Resolution.SHA.
//   - Requests carry an "Authorization: Bearer <token>" header when a token is
//     provided, and no Authorization header otherwise.
//   - A non-2xx response becomes a non-nil error whose message includes the
//     response status code.
//
// The client is constructed via github.New, pointed at the fake server via a
// github.WithBaseURL(baseURL) option.
var _ = Describe("Client", func() {
	const (
		repository = "owner/repo"
		prNumber   = int64(42)
		headSHA    = "abc123def456"
		commitSHA  = "deadbeefcafe0"
		diffBody   = "diff --git a/foo b/foo\n+bar\n"
	)

	wantsDiff := func(r *http.Request) bool {
		return strings.Contains(r.Header.Get("Accept"), "diff")
	}

	Describe("ResolvePR", func() {
		It("resolves the head sha from JSON and the diff from the pull request diff endpoint", func() {
			var gotJSONPath, gotDiffPath, gotDiffAccept string

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case fmt.Sprintf("/repos/%s/pulls/%d", repository, prNumber):
					Expect(r.Method).To(Equal(http.MethodGet))
					if wantsDiff(r) {
						gotDiffPath = r.URL.Path
						gotDiffAccept = r.Header.Get("Accept")
						fmt.Fprint(w, diffBody)
						return
					}
					gotJSONPath = r.URL.Path
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{"head":{"sha":%q}}`, headSHA)
				default:
					w.WriteHeader(http.StatusNotFound)
					fmt.Fprintf(w, "unexpected path: %s", r.URL.Path)
				}
			}))
			defer server.Close()

			client := github.New(github.WithBaseURL(server.URL))

			resolution, err := client.ResolvePR(context.Background(), repository, prNumber, "")

			Expect(err).NotTo(HaveOccurred())
			Expect(gotJSONPath).To(Equal(fmt.Sprintf("/repos/%s/pulls/%d", repository, prNumber)))
			Expect(gotDiffPath).To(Equal(fmt.Sprintf("/repos/%s/pulls/%d", repository, prNumber)))
			Expect(gotDiffAccept).To(ContainSubstring("diff"))
			Expect(resolution.SHA).To(Equal(headSHA))
			Expect(resolution.Diff).To(Equal(diffBody))
		})

		It("sends a bearer authorization header on both requests when a token is set", func() {
			var gotJSONAuth, gotDiffAuth string

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if wantsDiff(r) {
					gotDiffAuth = r.Header.Get("Authorization")
					fmt.Fprint(w, diffBody)
					return
				}
				gotJSONAuth = r.Header.Get("Authorization")
				fmt.Fprintf(w, `{"head":{"sha":%q}}`, headSHA)
			}))
			defer server.Close()

			client := github.New(github.WithBaseURL(server.URL))

			_, err := client.ResolvePR(context.Background(), repository, prNumber, "s3cr3t")

			Expect(err).NotTo(HaveOccurred())
			Expect(gotJSONAuth).To(Equal("Bearer s3cr3t"))
			Expect(gotDiffAuth).To(Equal("Bearer s3cr3t"))
		})

		It("omits the authorization header when no token is set", func() {
			sawAuthHeader := false

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" {
					sawAuthHeader = true
				}
				if wantsDiff(r) {
					fmt.Fprint(w, diffBody)
					return
				}
				fmt.Fprintf(w, `{"head":{"sha":%q}}`, headSHA)
			}))
			defer server.Close()

			client := github.New(github.WithBaseURL(server.URL))

			_, err := client.ResolvePR(context.Background(), repository, prNumber, "")

			Expect(err).NotTo(HaveOccurred())
			Expect(sawAuthHeader).To(BeFalse())
		})

		It("returns an error including the status code for a non-2xx response resolving the pull request", func() {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprint(w, `{"message":"Not Found"}`)
			}))
			defer server.Close()

			client := github.New(github.WithBaseURL(server.URL))

			_, err := client.ResolvePR(context.Background(), repository, prNumber, "")

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("404"))
		})

		It("returns an error including the status code for a non-2xx response fetching the diff", func() {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if wantsDiff(r) {
					w.WriteHeader(http.StatusInternalServerError)
					fmt.Fprint(w, "boom")
					return
				}
				fmt.Fprintf(w, `{"head":{"sha":%q}}`, headSHA)
			}))
			defer server.Close()

			client := github.New(github.WithBaseURL(server.URL))

			_, err := client.ResolvePR(context.Background(), repository, prNumber, "")

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("500"))
		})
	})

	Describe("ResolveCommit", func() {
		It("fetches the diff for the given sha and returns it with the sha unchanged", func() {
			var gotPath, gotAccept string

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotAccept = r.Header.Get("Accept")
				Expect(r.Method).To(Equal(http.MethodGet))
				fmt.Fprint(w, diffBody)
			}))
			defer server.Close()

			client := github.New(github.WithBaseURL(server.URL))

			resolution, err := client.ResolveCommit(context.Background(), repository, commitSHA, "")

			Expect(err).NotTo(HaveOccurred())
			Expect(gotPath).To(Equal(fmt.Sprintf("/repos/%s/commits/%s", repository, commitSHA)))
			Expect(gotAccept).To(ContainSubstring("diff"))
			Expect(resolution.SHA).To(Equal(commitSHA))
			Expect(resolution.Diff).To(Equal(diffBody))
		})

		It("sends a bearer authorization header when a token is set", func() {
			var gotAuth string

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotAuth = r.Header.Get("Authorization")
				fmt.Fprint(w, diffBody)
			}))
			defer server.Close()

			client := github.New(github.WithBaseURL(server.URL))

			_, err := client.ResolveCommit(context.Background(), repository, commitSHA, "s3cr3t")

			Expect(err).NotTo(HaveOccurred())
			Expect(gotAuth).To(Equal("Bearer s3cr3t"))
		})

		It("omits the authorization header when no token is set", func() {
			sawAuthHeader := false

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sawAuthHeader = r.Header.Get("Authorization") != ""
				fmt.Fprint(w, diffBody)
			}))
			defer server.Close()

			client := github.New(github.WithBaseURL(server.URL))

			_, err := client.ResolveCommit(context.Background(), repository, commitSHA, "")

			Expect(err).NotTo(HaveOccurred())
			Expect(sawAuthHeader).To(BeFalse())
		})

		It("returns an error including the status code for a non-2xx response", func() {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprint(w, `{"message":"Not Found"}`)
			}))
			defer server.Close()

			client := github.New(github.WithBaseURL(server.URL))

			_, err := client.ResolveCommit(context.Background(), repository, commitSHA, "")

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("404"))
		})
	})
})
