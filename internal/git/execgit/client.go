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
	"sync"

	providergit "github.com/UnstoppableMango/terraform-provider-git/internal/git"
)

//go:embed askpass.sh
var askpassScript []byte

// client is a git.Client implementation backed by the git binary. gitPath and
// the askpass script are resolved/written at most once per client instance
// and reused across calls, since both are invariant for the process's
// lifetime.
type client struct {
	gitPath string
	gitErr  error

	askpassOnce sync.Once
	askpassPath string
	askpassErr  error
}

// New returns a git.Client that shells out to the git binary found on PATH.
func New() providergit.Client {
	c := &client{}
	c.gitPath, c.gitErr = exec.LookPath("git")
	return c
}

func (c *client) LsRemote(ctx context.Context, url string, auth providergit.Auth) ([]providergit.Ref, error) {
	if c.gitErr != nil {
		return nil, fmt.Errorf("git binary not found: %w", c.gitErr)
	}

	cmd := exec.CommandContext(ctx, c.gitPath, "ls-remote", "--", url)
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0")

	if auth.Token != "" {
		scriptPath, err := c.askpassScriptPath()
		if err != nil {
			return nil, fmt.Errorf("preparing credentials: %w", err)
		}

		env = append(env,
			"GIT_ASKPASS="+scriptPath,
			"GIT_PROVIDER_TOKEN="+auth.Token,
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

// askpassScriptPath lazily writes the embedded askpass script to a temp file
// on first use and caches its path for the lifetime of the client.
func (c *client) askpassScriptPath() (string, error) {
	c.askpassOnce.Do(func() {
		c.askpassPath, c.askpassErr = writeAskpassScript()
	})
	return c.askpassPath, c.askpassErr
}

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

		parts := strings.SplitN(line, "\t", 2)
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
