// Package execgit implements the git.Client interface by shelling out to
// the real git binary on PATH.
package execgit

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	providergit "github.com/UnstoppableMango/terraform-provider-git/internal/git"
)

//go:embed askpass.sh
var askpassScript []byte

//go:embed ssh_wrapper.sh
var sshWrapperScript []byte

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
// wiring up a temporary GIT_ASKPASS script for a token, or a temporary
// GIT_SSH_COMMAND wrapper for SSH key-based auth. Callers must call the
// returned cleanup func once done.
func gitEnv(auth providergit.Auth) (env []string, cleanup func(), err error) {
	env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	cleanup = func() {}

	switch {
	case auth.SSHPrivateKey != "" || auth.SSHPrivateKeyPath != "":
		// The exec backend shells out to the real ssh binary via
		// GIT_SSH_COMMAND, which has no non-interactive way to supply a
		// passphrase without an external SSH agent already holding the key.
		// Fail clearly here rather than hanging on an interactive prompt
		// (GIT_TERMINAL_PROMPT=0 above only suppresses git's own prompts,
		// not ssh's host-key/passphrase prompts).
		if auth.SSHPassphrase != "" {
			return nil, nil, errors.New(`the exec git implementation cannot use a passphrase-protected SSH private key non-interactively; use git_implementation = "go-git", an unencrypted key, or an external SSH agent instead`)
		}

		keyPath := auth.SSHPrivateKeyPath
		if auth.SSHPrivateKey != "" {
			keyPath, err = writeTempKeyFile(auth.SSHPrivateKey)
			if err != nil {
				return nil, nil, err
			}
			cleanup = func() { _ = os.Remove(keyPath) }
		}

		wrapperPath, err := writeSSHWrapperScript()
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		prevCleanup := cleanup
		cleanup = func() { prevCleanup(); _ = os.Remove(wrapperPath) }

		env = append(env,
			"GIT_SSH_COMMAND="+wrapperPath,
			"GIT_PROVIDER_SSH_KEY="+keyPath,
		)
	case auth.Token != "":
		scriptPath, err := writeAskpassScript()
		if err != nil {
			return nil, nil, err
		}
		cleanup = func() { _ = os.Remove(scriptPath) }

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
	defer func() { _ = os.RemoveAll(workdir) }()

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
			_ = patchFile.Close()
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

	pushArgs := []string{"push", "origin", refspec}
	if req.ExpectedTip != "" {
		pushArgs = append(pushArgs, fmt.Sprintf("--force-with-lease=%s:%s", dstRef, req.ExpectedTip))
	} else {
		pushArgs = append(pushArgs, "--force")
	}

	if _, err := runGit(ctx, gitPath, cloneDir, env, pushArgs...); err != nil {
		if req.ExpectedTip != "" && isLeaseRejection(err) {
			return providergit.ApplyPatchesResult{}, &providergit.ConflictError{Branch: req.Branch, ExpectedTip: req.ExpectedTip, Err: err}
		}
		return providergit.ApplyPatchesResult{}, fmt.Errorf("pushing %s: %w", req.Branch, err)
	}

	return providergit.ApplyPatchesResult{ResolvedSHA: resolvedSHA}, nil
}

func (c *client) IsAncestor(ctx context.Context, url string, auth providergit.Auth, ancestor, descendant string) (bool, error) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return false, fmt.Errorf("git binary not found: %w", err)
	}

	env, cleanup, err := gitEnv(auth)
	if err != nil {
		return false, fmt.Errorf("preparing credentials: %w", err)
	}
	defer cleanup()

	workdir, err := os.MkdirTemp("", "terraform-provider-git-ancestor-*")
	if err != nil {
		return false, fmt.Errorf("creating workdir: %w", err)
	}
	defer func() { _ = os.RemoveAll(workdir) }()

	if _, err := runGit(ctx, gitPath, workdir, env, "init", "--quiet"); err != nil {
		return false, fmt.Errorf("initializing scratch repo: %w", err)
	}

	// Try fetching just the two commits we care about first (cheap: no
	// --depth here, since merge-base needs the full ancestry chain behind
	// descendant to find ancestor in it, but this still avoids fetching
	// every other branch). Some hosts reject fetching arbitrary SHAs
	// (uploadpack.allowReachableSHA1InWant disabled); if either fetch
	// fails, fall back to a full fetch of every branch so merge-base has
	// complete history to work with.
	_, ancestorErr := runGit(ctx, gitPath, workdir, env, "fetch", url, ancestor)
	_, descendantErr := runGit(ctx, gitPath, workdir, env, "fetch", url, descendant)
	if ancestorErr != nil || descendantErr != nil {
		if _, err := runGit(ctx, gitPath, workdir, env, "fetch", url, "+refs/heads/*:refs/remotes/origin/*"); err != nil {
			return false, fmt.Errorf("fetching history from %s: %w", url, err)
		}
	}

	return runMergeBaseIsAncestor(ctx, gitPath, workdir, env, ancestor, descendant)
}

// runMergeBaseIsAncestor reports whether ancestor is an ancestor of
// descendant among the objects already present in dir. A nonzero exit from
// `git merge-base --is-ancestor` (not an ancestor, or the object couldn't be
// found at all) is reported as (false, nil), not an error: from the caller's
// perspective both mean "no, ancestor is not reachable from descendant".
// Only a failure to run the command itself is a real error.
func runMergeBaseIsAncestor(ctx context.Context, gitPath, dir string, env []string, ancestor, descendant string) (bool, error) {
	cmd := exec.CommandContext(ctx, gitPath, "merge-base", "--is-ancestor", ancestor, descendant)
	cmd.Dir = dir
	cmd.Env = env

	err := cmd.Run()
	if err == nil {
		return true, nil
	}

	if _, ok := errors.AsType[*exec.ExitError](err); ok {
		return false, nil
	}

	return false, fmt.Errorf("merge-base --is-ancestor %s %s: %w", ancestor, descendant, err)
}

// isLeaseRejection reports whether err (as returned by runGit for a
// `--force-with-lease` push) looks like a lease rejection, i.e. the remote
// ref had already moved away from the expected tip, rather than some other
// push failure (network, auth, transport).
func isLeaseRejection(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "stale info") || strings.Contains(msg, "rejected")
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
		_ = f.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	if err := os.Chmod(path, 0o700); err != nil {
		_ = os.Remove(path)
		return "", err
	}

	return path, nil
}

// writeSSHWrapperScript writes the embedded SSH wrapper script (which reads
// the key path to use from GIT_PROVIDER_SSH_KEY, avoiding any shell
// interpolation of a user-supplied path) to a fresh temp file. Callers are
// responsible for removing the file once the git invocation using it has
// finished.
func writeSSHWrapperScript() (path string, err error) {
	f, err := os.CreateTemp("", "git-ssh-wrapper-*")
	if err != nil {
		return "", err
	}
	path = f.Name()

	if _, err := f.Write(sshWrapperScript); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	if err := os.Chmod(path, 0o700); err != nil {
		_ = os.Remove(path)
		return "", err
	}

	return path, nil
}

// writeTempKeyFile writes pemContent to a fresh temp file with the
// restrictive permissions ssh requires of a private key file. Callers are
// responsible for removing the file once the git invocation using it has
// finished.
func writeTempKeyFile(pemContent string) (path string, err error) {
	f, err := os.CreateTemp("", "git-ssh-key-*")
	if err != nil {
		return "", err
	}
	path = f.Name()

	if _, err := f.WriteString(pemContent); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = os.Remove(path)
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
