// Package local discovers a git repository already checked out on the local
// filesystem: the URL of one of its remotes, and the branch checked out.
//
// This sits outside the git.Client interface on purpose. Discovery is local,
// read-only, and needs neither auth nor network, so both access backends would
// implement it identically; go-git can read a repository's config regardless
// of which backend is configured for remote operations.
package local

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// DefaultRemote is the remote Discover reads when none is requested.
const DefaultRemote = "origin"

// Repository describes a repository checked out on the local filesystem.
type Repository struct {
	Root      string // absolute path of the working tree root; "" for a bare repository
	RemoteURL string // URL of the requested remote, exactly as git config records it
	HeadRef   string // short branch name; "" when HEAD is detached or unborn
	HeadSHA   string // commit HEAD points at; "" when HEAD is unborn
}

// NotARepositoryError indicates no git repository was found at or above the
// requested path. Callers use errors.As to tell this apart from failures
// reading a repository that does exist.
type NotARepositoryError struct {
	Path string
	Err  error
}

func (e *NotARepositoryError) Error() string {
	return fmt.Sprintf("no git repository found at or above %s: %v", e.Path, e.Err)
}

func (e *NotARepositoryError) Unwrap() error {
	return e.Err
}

// RemoteNotFoundError indicates the repository was opened but has no remote by
// the requested name. Available lists the remotes it does have, so callers can
// say what the user could have asked for instead.
type RemoteNotFoundError struct {
	Remote    string
	Root      string
	Available []string
	Err       error
}

func (e *RemoteNotFoundError) Error() string {
	if len(e.Available) == 0 {
		return fmt.Sprintf("repository at %s has no remote %q, and no remotes at all", e.Root, e.Remote)
	}

	return fmt.Sprintf("repository at %s has no remote %q; it has: %v", e.Root, e.Remote, e.Available)
}

func (e *RemoteNotFoundError) Unwrap() error {
	return e.Err
}

// Discover opens the repository containing path (walking up until a .git
// directory or file is found) and reports the URL of remote along with the
// branch currently checked out. An empty path means the process's working
// directory; an empty remote means DefaultRemote.
func Discover(path, remote string) (Repository, error) {
	if path == "" {
		path = "."
	}
	if remote == "" {
		remote = DefaultRemote
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return Repository{}, fmt.Errorf("resolving %s: %w", path, err)
	}

	// DetectDotGit walks up from abs; EnableDotGitCommonDir handles a .git
	// file pointing elsewhere, as in a linked worktree or a submodule.
	repo, err := git.PlainOpenWithOptions(abs, &git.PlainOpenOptions{
		DetectDotGit:          true,
		EnableDotGitCommonDir: true,
	})
	if err != nil {
		if errors.Is(err, git.ErrRepositoryNotExists) {
			return Repository{}, &NotARepositoryError{Path: abs, Err: err}
		}
		return Repository{}, fmt.Errorf("opening repository at %s: %w", abs, err)
	}

	result := Repository{Root: worktreeRoot(repo)}

	rem, err := repo.Remote(remote)
	if err != nil {
		if errors.Is(err, git.ErrRemoteNotFound) {
			return Repository{}, &RemoteNotFoundError{
				Remote:    remote,
				Root:      result.Root,
				Available: remoteNames(repo),
				Err:       err,
			}
		}
		return Repository{}, fmt.Errorf("reading remote %q: %w", remote, err)
	}

	urls := rem.Config().URLs
	if len(urls) == 0 {
		return Repository{}, fmt.Errorf("remote %q has no URL configured", remote)
	}
	result.RemoteURL = urls[0]

	head, err := repo.Head()
	switch {
	case errors.Is(err, plumbing.ErrReferenceNotFound):
		// Unborn branch: the repository has no commits yet. Not an error;
		// there is simply no head to report.
	case err != nil:
		return Repository{}, fmt.Errorf("reading HEAD: %w", err)
	default:
		result.HeadSHA = head.Hash().String()
		// A detached HEAD (what actions/checkout produces for pull_request
		// events) has no branch name to report.
		if head.Name().IsBranch() {
			result.HeadRef = head.Name().Short()
		}
	}

	return result, nil
}

// worktreeRoot returns the absolute path of repo's working tree, or "" when it
// has none (a bare repository).
func worktreeRoot(repo *git.Repository) string {
	wt, err := repo.Worktree()
	if err != nil {
		return ""
	}

	return wt.Filesystem.Root()
}

// remoteNames lists the names of repo's configured remotes, best-effort: a
// failure to read them only makes an error message less helpful.
func remoteNames(repo *git.Repository) []string {
	remotes, err := repo.Remotes()
	if err != nil {
		return nil
	}

	names := make([]string, 0, len(remotes))
	for _, r := range remotes {
		names = append(names, r.Config().Name)
	}

	return names
}
