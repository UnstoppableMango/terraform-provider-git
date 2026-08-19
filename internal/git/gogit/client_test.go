package gogit_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

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
		It("returns an error for an unreachable repository", func() {
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

		It("applies a single patch cleanly and force-pushes the result", func() {
			origin := newTestRepo()
			baseSHA := revParse(origin, "HEAD")
			patch1, _ := makeSequentialPatch(origin, "README.md", "one\n")

			client := gogit.New()
			result, err := client.ApplyPatches(context.Background(), providergit.ApplyPatchesRequest{
				URL:     "file://" + origin,
				Branch:  "feature",
				BaseRef: baseSHA,
				Patches: []string{patch1},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result.ResolvedSHA).NotTo(BeEmpty())

			Expect(showRefExists(origin, "refs/heads/feature")).To(BeTrue())
			originTip := revParse(origin, "refs/heads/feature")
			Expect(result.ResolvedSHA).To(Equal(originTip))

			content := showFileAtRef(origin, "refs/heads/feature", "README.md")
			Expect(content).To(Equal("one\n"))
		})

		It("applies multiple patches in order", func() {
			origin := newTestRepo()
			baseSHA := revParse(origin, "HEAD")
			patch1, scratch := makeSequentialPatch(origin, "README.md", "one\n")
			patch2 := makeFollowUpPatch(scratch, "README.md", "one\ntwo\n")

			client := gogit.New()
			result, err := client.ApplyPatches(context.Background(), providergit.ApplyPatchesRequest{
				URL:     "file://" + origin,
				Branch:  "feature-multi",
				BaseRef: baseSHA,
				Patches: []string{patch1, patch2},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result.ResolvedSHA).NotTo(BeEmpty())

			originTip := revParse(origin, "refs/heads/feature-multi")
			Expect(result.ResolvedSHA).To(Equal(originTip))

			content := showFileAtRef(origin, "refs/heads/feature-multi", "README.md")
			Expect(content).To(Equal("one\ntwo\n"))

			count := commitCount(origin, baseSHA, "refs/heads/feature-multi")
			Expect(count).To(Equal(2))
		})

		It("returns an error and leaves the remote unchanged when a patch fails to apply", func() {
			origin := newTestRepo()
			baseSHA := revParse(origin, "HEAD")

			badPatch := "diff --git a/README.md b/README.md\n" +
				"index 0000000..1111111 100644\n" +
				"--- a/README.md\n" +
				"+++ b/README.md\n" +
				"@@ -1,1 +1,1 @@\n" +
				"-this content does not exist in the file\n" +
				"+replacement\n"

			refsBefore := showRefs(origin)

			client := gogit.New()
			_, err := client.ApplyPatches(context.Background(), providergit.ApplyPatchesRequest{
				URL:     "file://" + origin,
				Branch:  "feature-fail",
				BaseRef: baseSHA,
				Patches: []string{badPatch},
			})

			Expect(err).To(HaveOccurred())
			Expect(showRefExists(origin, "refs/heads/feature-fail")).To(BeFalse())
			Expect(showRefs(origin)).To(Equal(refsBefore))
		})

		It("returns an error and leaves the remote unchanged when a delete patch's context doesn't match the current file", func() {
			origin := newTestRepo()
			baseSHA := revParse(origin, "HEAD")

			badDeletePatch := "diff --git a/README.md b/README.md\n" +
				"deleted file mode 100644\n" +
				"index 0000000..1111111 100644\n" +
				"--- a/README.md\n" +
				"+++ /dev/null\n" +
				"@@ -1,1 +0,0 @@\n" +
				"-this content does not exist in the file\n"

			refsBefore := showRefs(origin)

			client := gogit.New()
			_, err := client.ApplyPatches(context.Background(), providergit.ApplyPatchesRequest{
				URL:     "file://" + origin,
				Branch:  "feature-delete-fail",
				BaseRef: baseSHA,
				Patches: []string{badDeletePatch},
			})

			Expect(err).To(HaveOccurred())
			Expect(showRefExists(origin, "refs/heads/feature-delete-fail")).To(BeFalse())
			Expect(showRefs(origin)).To(Equal(refsBefore))
		})
	})
})

// makeSequentialPatch clones origin to a scratch directory, commits a change
// to path, and returns the diff plus the scratch directory for makeFollowUpPatch.
func makeSequentialPatch(origin, path, newContent string) (patch string, scratchDir string) {
	scratchDir = GinkgoT().TempDir()
	runGit("", "clone", origin, scratchDir)
	runGit(scratchDir, "config", "user.name", "Test User")
	runGit(scratchDir, "config", "user.email", "test@example.com")
	runGit(scratchDir, "config", "commit.gpgsign", "false")

	fullPath := filepath.Join(scratchDir, path)
	Expect(os.WriteFile(fullPath, []byte(newContent), 0o644)).To(Succeed())
	runGit(scratchDir, "add", path)
	runGit(scratchDir, "commit", "-m", "scratch commit")

	patch = gitDiff(scratchDir, "HEAD~1", "HEAD")
	return patch, scratchDir
}

// makeFollowUpPatch continues committing in an existing scratch clone
// (produced by makeSequentialPatch), changing path's content to newContent,
// and returns the diff between the previous and new commit.
func makeFollowUpPatch(scratchDir, path, newContent string) string {
	fullPath := filepath.Join(scratchDir, path)
	Expect(os.WriteFile(fullPath, []byte(newContent), 0o644)).To(Succeed())
	runGit(scratchDir, "add", path)
	runGit(scratchDir, "commit", "-m", "scratch commit 2")

	return gitDiff(scratchDir, "HEAD~1", "HEAD")
}

func gitDiff(dir, revA, revB string) string {
	cmd := exec.Command("git", "diff", revA, revB)
	cmd.Dir = dir
	out, err := cmd.Output()
	Expect(err).NotTo(HaveOccurred())
	return string(out)
}

func revParse(dir, rev string) string {
	cmd := exec.Command("git", "rev-parse", rev)
	cmd.Dir = dir
	out, err := cmd.Output()
	Expect(err).NotTo(HaveOccurred())
	return strings.TrimSpace(string(out))
}

func showRefExists(dir, ref string) bool {
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", ref)
	cmd.Dir = dir
	return cmd.Run() == nil
}

func showRefs(dir string) string {
	cmd := exec.Command("git", "show-ref")
	cmd.Dir = dir
	out, _ := cmd.Output()
	return string(out)
}

func showFileAtRef(dir, ref, path string) string {
	cmd := exec.Command("git", "show", ref+":"+path)
	cmd.Dir = dir
	out, err := cmd.Output()
	Expect(err).NotTo(HaveOccurred())
	return string(out)
}

func commitCount(dir, fromRev, toRev string) int {
	cmd := exec.Command("git", "rev-list", "--count", fromRev+".."+toRev)
	cmd.Dir = dir
	out, err := cmd.Output()
	Expect(err).NotTo(HaveOccurred())
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	Expect(err).NotTo(HaveOccurred())
	return n
}
