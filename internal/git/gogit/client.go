// Package gogit implements the git.Client interface using the pure-Go
// github.com/go-git/go-git/v5 library.
package gogit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	providergit "github.com/UnstoppableMango/terraform-provider-git/internal/git"
)

// client is a git.Client implementation backed by go-git.
type client struct{}

// New returns a git.Client that uses go-git as its access backend.
func New() providergit.Client {
	return &client{}
}

func (c *client) LsRemote(ctx context.Context, url string, auth providergit.Auth) ([]providergit.Ref, error) {
	method, err := authMethod(auth)
	if err != nil {
		return nil, fmt.Errorf("preparing SSH auth: %w", err)
	}

	remote := git.NewRemote(memory.NewStorage(), &config.RemoteConfig{
		Name: "origin",
		URLs: []string{url},
	})

	tflog.Debug(ctx, "listing remote refs", map[string]any{"url": url})

	refs, err := remote.ListContext(ctx, &git.ListOptions{
		Auth:          method,
		PeelingOption: git.AppendPeeled,
	})
	if err != nil {
		return nil, fmt.Errorf("ls-remote %s: %w", url, err)
	}

	result := make([]providergit.Ref, 0, len(refs))
	for _, ref := range refs {
		result = append(result, providergit.Ref{
			Name: ref.Name().String(),
			Hash: ref.Hash().String(),
		})
	}

	tflog.Debug(ctx, "listed remote refs", map[string]any{"url": url, "ref_count": len(result)})

	return result, nil
}

func (c *client) ApplyPatches(ctx context.Context, req providergit.ApplyPatchesRequest) (providergit.ApplyPatchesResult, error) {
	method, err := authMethod(req.Auth)
	if err != nil {
		return providergit.ApplyPatchesResult{}, fmt.Errorf("preparing SSH auth: %w", err)
	}

	tflog.Debug(ctx, "cloning repository", map[string]any{"url": req.URL, "branch": req.Branch})

	repo, err := git.CloneContext(ctx, memory.NewStorage(), memfs.New(), &git.CloneOptions{
		URL:  req.URL,
		Auth: method,
	})
	if err != nil {
		return providergit.ApplyPatchesResult{}, fmt.Errorf("cloning %s: %w", req.URL, err)
	}

	wt, err := repo.Worktree()
	if err != nil {
		return providergit.ApplyPatchesResult{}, fmt.Errorf("getting worktree: %w", err)
	}

	// SetReference (not Checkout{Create: true}) mirrors `git checkout -B`,
	// resetting req.Branch to req.BaseRef even if it already exists locally.
	branchRef := plumbing.NewBranchReferenceName(req.Branch)
	baseHash := plumbing.NewHash(req.BaseRef)
	if err := repo.Storer.SetReference(plumbing.NewHashReference(branchRef, baseHash)); err != nil {
		return providergit.ApplyPatchesResult{}, fmt.Errorf("checking out %s from %s: %w", req.Branch, req.BaseRef, err)
	}
	if err := wt.Checkout(&git.CheckoutOptions{Branch: branchRef, Force: true}); err != nil {
		return providergit.ApplyPatchesResult{}, fmt.Errorf("checking out %s from %s: %w", req.Branch, req.BaseRef, err)
	}

	resolvedHash := baseHash

	for i, patch := range req.Patches {
		tflog.Debug(ctx, "applying patch", map[string]any{"patch_index": i + 1, "patch_count": len(req.Patches)})

		files, _, err := gitdiff.Parse(strings.NewReader(patch))
		if err != nil {
			return providergit.ApplyPatchesResult{}, fmt.Errorf("parsing patch %d: %w", i+1, err)
		}

		for _, file := range files {
			if err := applyFile(wt, file); err != nil {
				return providergit.ApplyPatchesResult{}, fmt.Errorf("applying patch %d: %w", i+1, err)
			}
		}

		hash, err := wt.Commit(fmt.Sprintf("Apply patch %d", i+1), &git.CommitOptions{
			Author: &object.Signature{
				Name:  providergit.CommitAuthorName,
				Email: providergit.CommitAuthorEmail,
				When:  time.Now(),
			},
		})
		if err != nil {
			return providergit.ApplyPatchesResult{}, fmt.Errorf("committing patch %d: %w", i+1, err)
		}
		resolvedHash = hash
	}

	refspec := config.RefSpec(fmt.Sprintf("+%s:refs/heads/%s", branchRef, req.Branch))
	pushOpts := &git.PushOptions{
		RemoteName: "origin",
		RefSpecs:   []config.RefSpec{refspec},
		Auth:       method,
	}
	pushFields := map[string]any{"url": req.URL, "branch": req.Branch, "push_mode": "force"}
	if req.ExpectedTip != "" {
		pushOpts.ForceWithLease = &git.ForceWithLease{
			RefName: plumbing.NewBranchReferenceName(req.Branch),
			Hash:    plumbing.NewHash(req.ExpectedTip),
		}
		pushFields["push_mode"] = "force_with_lease"
		pushFields["expected_tip"] = req.ExpectedTip
	} else {
		pushOpts.Force = true
	}

	tflog.Debug(ctx, "pushing branch", pushFields)

	err = repo.PushContext(ctx, pushOpts)
	if err != nil {
		if req.ExpectedTip != "" && isLeaseRejection(err) {
			return providergit.ApplyPatchesResult{}, &providergit.ConflictError{Branch: req.Branch, ExpectedTip: req.ExpectedTip, Err: err}
		}
		return providergit.ApplyPatchesResult{}, fmt.Errorf("pushing %s: %w", req.Branch, err)
	}

	tflog.Debug(ctx, "applied patch stack", map[string]any{"url": req.URL, "branch": req.Branch, "patch_count": len(req.Patches), "resolved_sha": resolvedHash.String()})

	return providergit.ApplyPatchesResult{ResolvedSHA: resolvedHash.String()}, nil
}

// isLeaseRejection reports whether err (as returned by go-git's PushContext
// for a ForceWithLease push) looks like a lease rejection, i.e. the remote
// ref had already moved away from the expected tip, rather than some other
// push failure (network, auth, transport).
func isLeaseRejection(err error) bool {
	return errors.Is(err, git.ErrForceNeeded) || errors.Is(err, git.ErrNonFastForwardUpdate) ||
		strings.Contains(err.Error(), "non-fast-forward")
}

func (c *client) IsAncestor(ctx context.Context, url string, auth providergit.Auth, ancestor, descendant string) (bool, error) {
	ancestorHash := plumbing.NewHash(ancestor)
	descendantHash := plumbing.NewHash(descendant)
	if ancestorHash == descendantHash {
		return true, nil
	}

	method, err := authMethod(auth)
	if err != nil {
		return false, fmt.Errorf("preparing SSH auth: %w", err)
	}

	storer := memory.NewStorage()
	remote := git.NewRemote(storer, &config.RemoteConfig{
		Name: "origin",
		URLs: []string{url},
	})

	tflog.Debug(ctx, "fetching ancestry", map[string]any{"url": url, "ancestor": ancestor, "descendant": descendant})

	// Try fetching just the two commits we care about first (cheap). Some
	// hosts reject fetching arbitrary SHAs; if that fails, fall back to a
	// full fetch of every branch so ancestry can be checked against
	// complete history.
	shaErr := remote.FetchContext(ctx, &git.FetchOptions{
		Auth: method,
		RefSpecs: []config.RefSpec{
			config.RefSpec(ancestor + ":refs/ancestor"),
			config.RefSpec(descendant + ":refs/descendant"),
		},
	})
	if shaErr != nil && !errors.Is(shaErr, git.NoErrAlreadyUpToDate) {
		tflog.Debug(ctx, "falling back to full history fetch", map[string]any{"url": url})

		fullErr := remote.FetchContext(ctx, &git.FetchOptions{
			Auth:     method,
			RefSpecs: []config.RefSpec{"+refs/heads/*:refs/remotes/origin/*"},
		})
		if fullErr != nil && !errors.Is(fullErr, git.NoErrAlreadyUpToDate) {
			return false, fmt.Errorf("fetching history from %s: %w", url, fullErr)
		}
	}

	// A commit that couldn't be found even after the fallback fetch is
	// treated the same as "not an ancestor" (rewritten away / GC'd), not as
	// an error — see the IsAncestor doc comment on git.Client.
	ancestorCommit, err := object.GetCommit(storer, ancestorHash)
	if err != nil {
		return false, nil
	}
	descendantCommit, err := object.GetCommit(storer, descendantHash)
	if err != nil {
		return false, nil
	}

	isAncestor, err := ancestorCommit.IsAncestor(descendantCommit)
	if err != nil {
		return false, fmt.Errorf("checking ancestry: %w", err)
	}

	tflog.Debug(ctx, "checked ancestry", map[string]any{"url": url, "ancestor": ancestor, "descendant": descendant, "is_ancestor": isAncestor})

	return isAncestor, nil
}

// applyFile applies a single parsed diff file to the worktree's filesystem
// and stages the result, like `git apply --index` for one file in the patch.
func applyFile(wt *git.Worktree, file *gitdiff.File) error {
	if file.IsBinary {
		return fmt.Errorf("binary patches are not supported by the go-git backend: %s", file.NewName)
	}

	var src []byte
	if !file.IsNew {
		f, err := wt.Filesystem.Open(file.OldName)
		if err != nil {
			return fmt.Errorf("reading %s: %w", file.OldName, err)
		}
		src, err = io.ReadAll(f)
		_ = f.Close()
		if err != nil {
			return fmt.Errorf("reading %s: %w", file.OldName, err)
		}
	}

	if file.IsDelete {
		if err := gitdiff.Apply(io.Discard, bytes.NewReader(src), file); err != nil {
			return fmt.Errorf("applying changes to %s: %w", file.OldName, err)
		}
		if _, err := wt.Remove(file.OldName); err != nil {
			return fmt.Errorf("removing %s: %w", file.OldName, err)
		}
		return nil
	}

	dir := path.Dir(file.NewName)
	if dir != "." {
		if err := wt.Filesystem.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating directory for %s: %w", file.NewName, err)
		}
	}

	dst, err := wt.Filesystem.Create(file.NewName)
	if err != nil {
		return fmt.Errorf("writing %s: %w", file.NewName, err)
	}
	applyErr := gitdiff.Apply(dst, bytes.NewReader(src), file)
	closeErr := dst.Close()
	if applyErr != nil {
		return fmt.Errorf("applying changes to %s: %w", file.NewName, applyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("writing %s: %w", file.NewName, closeErr)
	}

	if _, err := wt.Add(file.NewName); err != nil {
		return fmt.Errorf("staging %s: %w", file.NewName, err)
	}

	if file.IsRename && file.OldName != file.NewName {
		if _, err := wt.Remove(file.OldName); err != nil {
			return fmt.Errorf("removing %s: %w", file.OldName, err)
		}
	}

	return nil
}

func authMethod(auth providergit.Auth) (transport.AuthMethod, error) {
	user := auth.SSHUser
	if user == "" {
		user = "git"
	}

	switch {
	case auth.SSHPrivateKey != "":
		return ssh.NewPublicKeys(user, []byte(auth.SSHPrivateKey), auth.SSHPassphrase)
	case auth.SSHPrivateKeyPath != "":
		return ssh.NewPublicKeysFromFile(user, auth.SSHPrivateKeyPath, auth.SSHPassphrase)
	case auth.SSHAgent:
		return ssh.NewSSHAgentAuth(user)
	case auth.Token != "":
		return &http.BasicAuth{
			Username: providergit.Username(auth.Host),
			Password: auth.Token,
		}, nil
	default:
		return nil, nil
	}
}
