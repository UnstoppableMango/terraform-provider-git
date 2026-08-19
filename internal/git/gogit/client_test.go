package gogit_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	providergit "github.com/UnstoppableMango/terraform-provider-git/internal/git"
	"github.com/UnstoppableMango/terraform-provider-git/internal/git/gogit"
)

var _ = Describe("Client", func() {
	Describe("LsRemote", func() {
		It("lists refs from a reachable local repository", func() {
			repoPath := newTestRepo()
			client := gogit.New()

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
			client := gogit.New()

			_, err := client.LsRemote(context.Background(), "file:///nonexistent/path/xyz", providergit.Auth{})

			Expect(err).To(HaveOccurred())
		})
	})

	Describe("ApplyPatches", func() {
		It("returns a not-implemented error", func() {
			client := gogit.New()

			result, err := client.ApplyPatches(context.Background(), providergit.ApplyPatchesRequest{
				URL:     "file:///nonexistent/path/xyz",
				Branch:  "main",
				BaseRef: "deadbeef",
				Patches: []string{"diff --git a/foo b/foo"},
			})

			Expect(err).To(HaveOccurred())
			Expect(result).To(Equal(providergit.ApplyPatchesResult{}))
		})
	})
})
