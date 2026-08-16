package execgit_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestExecgit(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "internal/git/execgit Suite")
}

// newTestRepo creates a temporary, non-bare git repository with a single
// commit and returns its filesystem path. It uses repo-local git config
// (not global) so the commit doesn't depend on the host's git identity
// being configured.
//
// This duplicates the equivalent helper in internal/git/gogit's tests
// intentionally, to keep each backend's test fixtures independent.
func newTestRepo() string {
	dir := GinkgoT().TempDir()

	runGit(dir, "init")
	runGit(dir, "config", "user.name", "Test User")
	runGit(dir, "config", "user.email", "test@example.com")
	runGit(dir, "config", "commit.gpgsign", "false")
	runGit(dir, "config", "tag.gpgsign", "false")

	readmePath := filepath.Join(dir, "README.md")
	Expect(os.WriteFile(readmePath, []byte("test\n"), 0o644)).To(Succeed())

	runGit(dir, "add", "README.md")
	runGit(dir, "commit", "-m", "initial commit")

	return dir
}

func runGit(dir string, args ...string) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), string(out))
}
