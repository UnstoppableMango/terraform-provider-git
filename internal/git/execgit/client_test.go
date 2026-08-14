package execgit_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	providergit "github.com/UnstoppableMango/terraform-provider-git/internal/git"
	"github.com/UnstoppableMango/terraform-provider-git/internal/git/execgit"
)

var _ = Describe("Client", func() {
	Describe("LsRemote", func() {
		It("lists refs from a reachable local repository", func() {
			repoPath := newTestRepo()
			client := execgit.New()

			refs, err := client.LsRemote(context.Background(), "file://"+repoPath, providergit.Auth{})

			Expect(err).NotTo(HaveOccurred())
			Expect(refs).NotTo(BeEmpty())

			names := make([]string, 0, len(refs))
			for _, ref := range refs {
				names = append(names, ref.Name)
			}
			Expect(names).To(ContainElement(SatisfyAny(
				Equal("HEAD"),
				ContainSubstring("refs/heads/"),
			)))
		})

		It("returns an error for an unreachable repository", func() {
			client := execgit.New()

			_, err := client.LsRemote(context.Background(), "file:///nonexistent/path/xyz", providergit.Auth{})

			Expect(err).To(HaveOccurred())
		})

		It("succeeds against an unauthenticated repository even when a token is supplied", func() {
			repoPath := newTestRepo()
			client := execgit.New()

			refs, err := client.LsRemote(context.Background(), "file://"+repoPath, providergit.Auth{Token: "unused-token"})

			Expect(err).NotTo(HaveOccurred())
			Expect(refs).NotTo(BeEmpty())
		})
	})
})
