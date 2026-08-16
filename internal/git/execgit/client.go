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

	cmd := exec.CommandContext(ctx, gitPath, "ls-remote", "--", url)
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0")

	if auth.Token != "" {
		scriptPath, err := writeAskpassScript()
		if err != nil {
			return nil, fmt.Errorf("preparing credentials: %w", err)
		}
		defer os.Remove(scriptPath)

		env = append(env,
			"GIT_ASKPASS="+scriptPath,
			"GIT_PROVIDER_TOKEN="+auth.Token,
			"GIT_PROVIDER_USERNAME="+providergit.Username(auth.Host),
		)
	}

	cmd.Env = env

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ls-remote %s: %w: %s", url, err, strings.TrimSpace(stderr.String()))
	}

	return parseLsRemote(stdout.String()), nil
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
