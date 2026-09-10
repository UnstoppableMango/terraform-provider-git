package local_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/UnstoppableMango/terraform-provider-git/internal/git/local"
)

// newRepo creates a temporary git repository with one commit and an "origin"
// remote pointing at url, returning its path. Real git is used rather than
// go-git so the fixtures match what a user's checkout actually looks like.
func newRepo(url string) string {
	GinkgoHelper()

	dir := GinkgoT().TempDir()

	runGit(dir, "init")
	runGit(dir, "config", "user.name", "Test User")
	runGit(dir, "config", "user.email", "test@example.com")
	runGit(dir, "config", "commit.gpgsign", "false")
	runGit(dir, "remote", "add", "origin", url)

	Expect(os.WriteFile(filepath.Join(dir, "README.md"), []byte("test\n"), 0o644)).To(Succeed())
	runGit(dir, "add", "README.md")
	runGit(dir, "commit", "-m", "initial commit")

	return dir
}

func runGit(dir string, args ...string) string {
	GinkgoHelper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "git %v: %s", args, out)

	return string(out)
}

var _ = Describe("Discover", func() {
	const originURL = "git@github.com:owner/repo.git"

	It("reads the origin URL and checked-out branch", func() {
		dir := newRepo(originURL)

		repo, err := local.Discover(dir, "")

		Expect(err).NotTo(HaveOccurred())
		Expect(repo.RemoteURL).To(Equal(originURL))
		Expect(repo.HeadRef).To(Equal(currentBranch(dir)))
		Expect(repo.HeadSHA).To(HaveLen(40))
		Expect(repo.Root).NotTo(BeEmpty())
	})

	It("walks up from a subdirectory", func() {
		dir := newRepo(originURL)
		nested := filepath.Join(dir, "deeply", "nested")
		Expect(os.MkdirAll(nested, 0o755)).To(Succeed())

		repo, err := local.Discover(nested, "")

		Expect(err).NotTo(HaveOccurred())
		Expect(repo.RemoteURL).To(Equal(originURL))
	})

	It("reads a remote other than origin", func() {
		dir := newRepo(originURL)
		runGit(dir, "remote", "add", "upstream", "https://example.com/upstream.git")

		repo, err := local.Discover(dir, "upstream")

		Expect(err).NotTo(HaveOccurred())
		Expect(repo.RemoteURL).To(Equal("https://example.com/upstream.git"))
	})

	It("reports a detached HEAD as having no branch", func() {
		dir := newRepo(originURL)
		sha := runGit(dir, "rev-parse", "HEAD")
		runGit(dir, "checkout", "--detach", sha[:40])

		repo, err := local.Discover(dir, "")

		Expect(err).NotTo(HaveOccurred())
		Expect(repo.HeadRef).To(BeEmpty())
		Expect(repo.HeadSHA).To(Equal(sha[:40]))
	})

	It("reports a repository with no commits as having no head", func() {
		dir := GinkgoT().TempDir()
		runGit(dir, "init")
		runGit(dir, "remote", "add", "origin", originURL)

		repo, err := local.Discover(dir, "")

		Expect(err).NotTo(HaveOccurred())
		Expect(repo.RemoteURL).To(Equal(originURL))
		Expect(repo.HeadRef).To(BeEmpty())
		Expect(repo.HeadSHA).To(BeEmpty())
	})

	It("returns a RemoteNotFoundError listing the remotes that do exist", func() {
		dir := newRepo(originURL)

		_, err := local.Discover(dir, "upstream")

		notFound, ok := errors.AsType[*local.RemoteNotFoundError](err)
		Expect(ok).To(BeTrue(), "expected a *local.RemoteNotFoundError, got %v", err)
		Expect(notFound.Remote).To(Equal("upstream"))
		Expect(notFound.Available).To(ConsistOf("origin"))
	})

	It("returns a NotARepositoryError outside a repository", func() {
		dir := GinkgoT().TempDir()

		_, err := local.Discover(dir, "")

		notRepo, ok := errors.AsType[*local.NotARepositoryError](err)
		Expect(ok).To(BeTrue(), "expected a *local.NotARepositoryError, got %v", err)
		Expect(notRepo.Path).To(ContainSubstring(filepath.Base(dir)))
	})
})

func currentBranch(dir string) string {
	GinkgoHelper()

	out := runGit(dir, "rev-parse", "--abbrev-ref", "HEAD")

	return out[:len(out)-1] // strip the trailing newline
}
