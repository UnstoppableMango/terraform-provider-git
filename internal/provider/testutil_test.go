package provider_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/UnstoppableMango/terraform-provider-git/internal/git/github"
)

// newTestRepo creates a temporary, non-bare git repository with a single
// commit and returns its filesystem path. It uses repo-local git config
// (not global) so the commit doesn't depend on the host's git identity
// being configured. The directory is removed via t.Cleanup.
//
// Shared by the git_repository resource tests and the provider-level
// acceptance tests, both of which need a real, reachable local repository
// now that Create/Read/Update call LsRemote.
func newTestRepo(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "git-repo-test-*")
	if err != nil {
		panic(fmt.Sprintf("newTestRepo: MkdirTemp: %v", err))
	}
	t.Cleanup(func() {
		os.RemoveAll(dir)
	})

	runGit(dir, "init")
	runGit(dir, "config", "user.name", "Test User")
	runGit(dir, "config", "user.email", "test@example.com")
	runGit(dir, "config", "commit.gpgsign", "false")
	runGit(dir, "config", "tag.gpgsign", "false")

	readmePath := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readmePath, []byte("test\n"), 0o644); err != nil {
		panic(fmt.Sprintf("newTestRepo: WriteFile: %v", err))
	}

	runGit(dir, "add", "README.md")
	runGit(dir, "commit", "-m", "initial commit")

	return dir
}

func runGit(dir string, args ...string) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		panic(fmt.Sprintf("newTestRepo: git %v: %v: %s", args, err, out))
	}
}

// fakeGitHubClient is a test double for github.Client. Each method is
// backed by a configurable function field; a nil field panics if called,
// so tests only need to set the functions relevant to what they exercise.
type fakeGitHubClient struct {
	resolvePRFunc     func(ctx context.Context, repository string, pr int64, token string) (github.Resolution, error)
	resolveCommitFunc func(ctx context.Context, repository, sha, token string) (github.Resolution, error)
}

var _ github.Client = (*fakeGitHubClient)(nil)

func (f *fakeGitHubClient) ResolvePR(ctx context.Context, repository string, pr int64, token string) (github.Resolution, error) {
	if f.resolvePRFunc == nil {
		panic("fakeGitHubClient: ResolvePR called but resolvePRFunc is nil")
	}
	return f.resolvePRFunc(ctx, repository, pr, token)
}

func (f *fakeGitHubClient) ResolveCommit(ctx context.Context, repository, sha, token string) (github.Resolution, error) {
	if f.resolveCommitFunc == nil {
		panic("fakeGitHubClient: ResolveCommit called but resolveCommitFunc is nil")
	}
	return f.resolveCommitFunc(ctx, repository, sha, token)
}
