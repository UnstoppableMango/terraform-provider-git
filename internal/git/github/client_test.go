package github_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/UnstoppableMango/terraform-provider-git/internal/git/github"
)

func TestGithub(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "internal/git/github Suite")
}

// The fake server below stands in for the real GitHub REST API and expects
// the client under test to speak the following contract:
//
//   - ResolvePR issues GET /repos/{owner}/{name}/pulls/{pr}, parses the head
//     commit sha from the JSON response body (shaped like the real GitHub API:
//     {"head":{"sha":"..."}}), then issues GET
//     /repos/{owner}/{name}/commits/{sha} with an
//     "Accept: application/vnd.github.v3.diff" header and uses the raw
//     response body as the diff.
//   - ResolveCommit issues GET /repos/{owner}/{name}/commits/{sha} directly
//     with the same diff Accept header and uses the raw response body as the
//     diff, returning the input sha unchanged as Resolution.SHA.
//   - Both requests carry an "Authorization: Bearer <token>" header when
//     Auth.Token is non-empty, and no Authorization header at all otherwise.
//   - A non-2xx response from either endpoint becomes a non-nil error whose
//     message includes the response status code.
//
// The client is expected to be constructed via github.New, pointed at the
// fake server via a github.WithBaseURL(baseURL) option.
var _ = Describe("Client", func() {
	const (
		repository = "owner/repo"
		prNumber   = int64(42)
		headSHA    = "abc123def456"
		commitSHA  = "deadbeefcafe0"
		diffBody   = "diff --git a/foo b/foo\n+bar\n"
	)

	Describe("ResolvePR", func() {
		It("requests the pull request at the expected path and resolves sha and diff", func() {
			var gotPRPath, gotDiffPath, gotDiffAccept string

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case fmt.Sprintf("/repos/%s/pulls/%d", repository, prNumber):
					gotPRPath = r.URL.Path
					Expect(r.Method).To(Equal(http.MethodGet))
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{"head":{"sha":%q}}`, headSHA)
				case fmt.Sprintf("/repos/%s/commits/%s", repository, headSHA):
					gotDiffPath = r.URL.Path
					gotDiffAccept = r.Header.Get("Accept")
					fmt.Fprint(w, diffBody)
				default:
					w.WriteHeader(http.StatusNotFound)
					fmt.Fprintf(w, "unexpected path: %s", r.URL.Path)
				}
			}))
			defer server.Close()

			client := github.New(github.WithBaseURL(server.URL))

			resolution, err := client.ResolvePR(context.Background(), repository, prNumber, github.Auth{})

			Expect(err).NotTo(HaveOccurred())
			Expect(gotPRPath).To(Equal(fmt.Sprintf("/repos/%s/pulls/%d", repository, prNumber)))
			Expect(gotDiffPath).To(Equal(fmt.Sprintf("/repos/%s/commits/%s", repository, headSHA)))
			Expect(gotDiffAccept).To(ContainSubstring("diff"))
			Expect(resolution.SHA).To(Equal(headSHA))
			Expect(resolution.Diff).To(Equal(diffBody))
		})

		It("sends a bearer authorization header on both requests when a token is set", func() {
			var gotPRAuth, gotDiffAuth string

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case fmt.Sprintf("/repos/%s/pulls/%d", repository, prNumber):
					gotPRAuth = r.Header.Get("Authorization")
					fmt.Fprintf(w, `{"head":{"sha":%q}}`, headSHA)
				default:
					gotDiffAuth = r.Header.Get("Authorization")
					fmt.Fprint(w, diffBody)
				}
			}))
			defer server.Close()

			client := github.New(github.WithBaseURL(server.URL))

			_, err := client.ResolvePR(context.Background(), repository, prNumber, github.Auth{Token: "s3cr3t"})

			Expect(err).NotTo(HaveOccurred())
			Expect(gotPRAuth).To(Equal("Bearer s3cr3t"))
			Expect(gotDiffAuth).To(Equal("Bearer s3cr3t"))
		})

		It("omits the authorization header when no token is set", func() {
			var gotPRAuth string
			sawPRAuthHeader := false

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case fmt.Sprintf("/repos/%s/pulls/%d", repository, prNumber):
					gotPRAuth = r.Header.Get("Authorization")
					sawPRAuthHeader = r.Header.Get("Authorization") != ""
					fmt.Fprintf(w, `{"head":{"sha":%q}}`, headSHA)
				default:
					fmt.Fprint(w, diffBody)
				}
			}))
			defer server.Close()

			client := github.New(github.WithBaseURL(server.URL))

			_, err := client.ResolvePR(context.Background(), repository, prNumber, github.Auth{})

			Expect(err).NotTo(HaveOccurred())
			Expect(sawPRAuthHeader).To(BeFalse())
			Expect(gotPRAuth).To(BeEmpty())
		})

		It("returns an error including the status code for a non-2xx response resolving the pull request", func() {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprint(w, `{"message":"Not Found"}`)
			}))
			defer server.Close()

			client := github.New(github.WithBaseURL(server.URL))

			_, err := client.ResolvePR(context.Background(), repository, prNumber, github.Auth{})

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("404"))
		})

		It("returns an error including the status code for a non-2xx response fetching the diff", func() {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case fmt.Sprintf("/repos/%s/pulls/%d", repository, prNumber):
					fmt.Fprintf(w, `{"head":{"sha":%q}}`, headSHA)
				default:
					w.WriteHeader(http.StatusInternalServerError)
					fmt.Fprint(w, "boom")
				}
			}))
			defer server.Close()

			client := github.New(github.WithBaseURL(server.URL))

			_, err := client.ResolvePR(context.Background(), repository, prNumber, github.Auth{})

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

			resolution, err := client.ResolveCommit(context.Background(), repository, commitSHA, github.Auth{})

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

			_, err := client.ResolveCommit(context.Background(), repository, commitSHA, github.Auth{Token: "s3cr3t"})

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

			_, err := client.ResolveCommit(context.Background(), repository, commitSHA, github.Auth{})

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

			_, err := client.ResolveCommit(context.Background(), repository, commitSHA, github.Auth{})

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("404"))
		})
	})
})
