// Package gogit implements the git.Client interface using the pure-Go
// github.com/go-git/go-git/v5 library.
package gogit

import (
	"bytes"
	"context"
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
	"github.com/go-git/go-git/v5/storage/memory"

	providergit "github.com/UnstoppableMango/terraform-provider-git/internal/git"
)

// client is a git.Client implementation backed by go-git.
type client struct{}

// New returns a git.Client that uses go-git as its access backend.
func New() providergit.Client {
	return &client{}
}

func (c *client) LsRemote(ctx context.Context, url string, auth providergit.Auth) ([]providergit.Ref, error) {
	remote := git.NewRemote(memory.NewStorage(), &config.RemoteConfig{
		Name: "origin",
		URLs: []string{url},
	})

	refs, err := remote.ListContext(ctx, &git.ListOptions{
		Auth:          authMethod(auth),
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

	return result, nil
}

func (c *client) ApplyPatches(ctx context.Context, req providergit.ApplyPatchesRequest) (providergit.ApplyPatchesResult, error) {
	repo, err := git.CloneContext(ctx, memory.NewStorage(), memfs.New(), &git.CloneOptions{
		URL:  req.URL,
		Auth: authMethod(req.Auth),
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
	err = repo.PushContext(ctx, &git.PushOptions{
		RemoteName: "origin",
		RefSpecs:   []config.RefSpec{refspec},
		Auth:       authMethod(req.Auth),
		Force:      true,
	})
	if err != nil {
		return providergit.ApplyPatchesResult{}, fmt.Errorf("pushing %s: %w", req.Branch, err)
	}

	return providergit.ApplyPatchesResult{ResolvedSHA: resolvedHash.String()}, nil
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

func authMethod(auth providergit.Auth) transport.AuthMethod {
	if auth.Token == "" {
		return nil
	}

	return &http.BasicAuth{
		Username: providergit.Username(auth.Host),
		Password: auth.Token,
	}
}
