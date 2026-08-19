// Package execgit implements the git.Client interface by shelling out to
// the real git binary on PATH.
package execgit

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	providergit "github.com/UnstoppableMango/terraform-provider-git/internal/git"
)

//go:embed askpass.sh
var askpassScript []byte

// client is a git.Client implementation backed by the git binary.
type client struct{}

// New returns a git.Client that shells out to the git binary found on PATH.
func New() providergit.Client {
	return &client{}
}

func (c *client) LsRemote(ctx context.Context, url string, auth providergit.Auth) ([]providergit.Ref, error) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("git binary not found: %w", err)
	}

	env, cleanup, err := gitEnv(auth)
	if err != nil {
		return nil, fmt.Errorf("preparing credentials: %w", err)
	}
	defer cleanup()

	cmd := exec.CommandContext(ctx, gitPath, "ls-remote", "--", url)
	cmd.Env = env

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ls-remote %s: %w: %s", url, err, strings.TrimSpace(stderr.String()))
	}

	return parseLsRemote(stdout.String()), nil
}

// gitEnv builds the environment for git invocations against a remote,
// wiring up a temporary GIT_ASKPASS script when a token is supplied.
// Callers must call the returned cleanup func once done.
func gitEnv(auth providergit.Auth) (env []string, cleanup func(), err error) {
	env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	cleanup = func() {}

	if auth.Token != "" {
		scriptPath, err := writeAskpassScript()
		if err != nil {
			return nil, nil, err
		}
		cleanup = func() { os.Remove(scriptPath) }

		env = append(env,
			"GIT_ASKPASS="+scriptPath,
			"GIT_PROVIDER_TOKEN="+auth.Token,
			"GIT_PROVIDER_USERNAME="+providergit.Username(auth.Host),
		)
	}

	return env, cleanup, nil
}

func (c *client) ApplyPatches(ctx context.Context, req providergit.ApplyPatchesRequest) (providergit.ApplyPatchesResult, error) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return providergit.ApplyPatchesResult{}, fmt.Errorf("git binary not found: %w", err)
	}

	env, cleanup, err := gitEnv(req.Auth)
	if err != nil {
		return providergit.ApplyPatchesResult{}, fmt.Errorf("preparing credentials: %w", err)
	}
	defer cleanup()

	workdir, err := os.MkdirTemp("", "terraform-provider-git-apply-*")
	if err != nil {
		return providergit.ApplyPatchesResult{}, fmt.Errorf("creating workdir: %w", err)
	}
	defer os.RemoveAll(workdir)

	cloneDir := filepath.Join(workdir, "repo")

	if _, err := runGit(ctx, gitPath, "", env, "clone", "--origin=origin", req.URL, cloneDir); err != nil {
		return providergit.ApplyPatchesResult{}, fmt.Errorf("cloning %s: %w", req.URL, err)
	}

	if _, err := runGit(ctx, gitPath, cloneDir, env, "config", "user.name", providergit.CommitAuthorName); err != nil {
		return providergit.ApplyPatchesResult{}, fmt.Errorf("setting commit identity: %w", err)
	}
	if _, err := runGit(ctx, gitPath, cloneDir, env, "config", "user.email", providergit.CommitAuthorEmail); err != nil {
		return providergit.ApplyPatchesResult{}, fmt.Errorf("setting commit identity: %w", err)
	}

	// -B resets req.Branch to req.BaseRef even if it already exists locally.
	if _, err := runGit(ctx, gitPath, cloneDir, env, "checkout", "-B", req.Branch, req.BaseRef); err != nil {
		return providergit.ApplyPatchesResult{}, fmt.Errorf("checking out %s from %s: %w", req.Branch, req.BaseRef, err)
	}

	for i, patch := range req.Patches {
		patchFile, err := os.CreateTemp(workdir, fmt.Sprintf("patch-%d-*.diff", i+1))
		if err != nil {
			return providergit.ApplyPatchesResult{}, fmt.Errorf("writing patch %d: %w", i+1, err)
		}
		patchPath := patchFile.Name()

		if _, err := patchFile.WriteString(patch); err != nil {
			patchFile.Close()
			return providergit.ApplyPatchesResult{}, fmt.Errorf("writing patch %d: %w", i+1, err)
		}
		if err := patchFile.Close(); err != nil {
			return providergit.ApplyPatchesResult{}, fmt.Errorf("writing patch %d: %w", i+1, err)
		}

		if _, err := runGit(ctx, gitPath, cloneDir, env, "apply", "--index", patchPath); err != nil {
			return providergit.ApplyPatchesResult{}, fmt.Errorf("applying patch %d: %w", i+1, err)
		}

		if _, err := runGit(ctx, gitPath, cloneDir, env, "commit", "-m", fmt.Sprintf("Apply patch %d", i+1)); err != nil {
			return providergit.ApplyPatchesResult{}, fmt.Errorf("committing patch %d: %w", i+1, err)
		}
	}

	sha, err := runGit(ctx, gitPath, cloneDir, env, "rev-parse", "HEAD")
	if err != nil {
		return providergit.ApplyPatchesResult{}, fmt.Errorf("resolving HEAD: %w", err)
	}
	resolvedSHA := strings.TrimSpace(sha)

	dstRef := req.Branch
	if !strings.HasPrefix(dstRef, "refs/") {
		dstRef = "refs/heads/" + dstRef
	}
	refspec := fmt.Sprintf("HEAD:%s", dstRef)
	if _, err := runGit(ctx, gitPath, cloneDir, env, "push", "--force", "origin", refspec); err != nil {
		return providergit.ApplyPatchesResult{}, fmt.Errorf("pushing %s: %w", req.Branch, err)
	}

	return providergit.ApplyPatchesResult{ResolvedSHA: resolvedSHA}, nil
}

// runGit runs git with the given args in dir (the process's own working
// directory if dir is empty) using env, returning stdout on success or a
// wrapped error including stderr on failure.
func runGit(ctx context.Context, gitPath, dir string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, gitPath, args...)
	cmd.Dir = dir
	cmd.Env = env

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}

	return stdout.String(), nil
}

// writeAskpassScript writes the embedded askpass script to a fresh temp file.
// Callers are responsible for removing the file once the git invocation
// using it has finished.
func writeAskpassScript() (path string, err error) {
	f, err := os.CreateTemp("", "git-askpass-*")
	if err != nil {
		return "", err
	}
	path = f.Name()

	if _, err := f.Write(askpassScript); err != nil {
		f.Close()
		os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	if err := os.Chmod(path, 0o700); err != nil {
		os.Remove(path)
		return "", err
	}

	return path, nil
}

func parseLsRemote(output string) []providergit.Ref {
	lines := strings.Split(output, "\n")
	refs := make([]providergit.Ref, 0, len(lines))

	for _, line := range lines {
		if line == "" {
			continue
		}

		parts := strings.SplitN(strings.TrimRight(line, "\r"), "\t", 2)
		if len(parts) != 2 {
			continue
		}

		refs = append(refs, providergit.Ref{
			Hash: parts[0],
			Name: parts[1],
		})
	}

	return refs
}
