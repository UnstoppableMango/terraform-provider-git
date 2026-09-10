package github

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	gogithub "github.com/google/go-github/v75/github"
)

// Option configures a client constructed via New.
type Option func(*client)

// WithBaseURL overrides the API base URL. Intended for tests to point the
// client at a fake server.
func WithBaseURL(u string) Option {
	return func(c *client) {
		c.baseURL = u
	}
}

// New creates a Client that talks to the GitHub REST API via go-github.
func New(opts ...Option) Client {
	c := &client{}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

type client struct {
	baseURL string
}

var _ Client = (*client)(nil)

func (c *client) ResolvePR(ctx context.Context, repository string, pr int64, token string) (Resolution, error) {
	owner, name, err := splitRepository(repository)
	if err != nil {
		return Resolution{}, err
	}

	gh, err := c.newGitHub(token)
	if err != nil {
		return Resolution{}, err
	}

	pull, _, err := gh.PullRequests.Get(ctx, owner, name, int(pr))
	if err != nil {
		return Resolution{}, fmt.Errorf("resolving pull request: %w", err)
	}

	sha := pull.GetHead().GetSHA()
	if sha == "" {
		return Resolution{}, fmt.Errorf("pull request response missing head commit sha")
	}

	diff, _, err := gh.PullRequests.GetRaw(ctx, owner, name, int(pr), gogithub.RawOptions{Type: gogithub.Diff})
	if err != nil {
		return Resolution{}, fmt.Errorf("fetching diff: %w", err)
	}

	return Resolution{SHA: sha, Diff: diff}, nil
}

func (c *client) ResolveCommit(ctx context.Context, repository, sha, token string) (Resolution, error) {
	owner, name, err := splitRepository(repository)
	if err != nil {
		return Resolution{}, err
	}

	gh, err := c.newGitHub(token)
	if err != nil {
		return Resolution{}, err
	}

	diff, _, err := gh.Repositories.GetCommitRaw(ctx, owner, name, sha, gogithub.RawOptions{Type: gogithub.Diff})
	if err != nil {
		return Resolution{}, fmt.Errorf("fetching diff: %w", err)
	}

	return Resolution{SHA: sha, Diff: diff}, nil
}

// newGitHub builds a go-github client, applying the optional base URL override
// and bearer token authentication when token is non-empty.
func (c *client) newGitHub(token string) (*gogithub.Client, error) {
	gh := gogithub.NewClient(nil)

	if token != "" {
		gh = gh.WithAuthToken(token)
	}

	if c.baseURL != "" {
		base, err := url.Parse(c.baseURL)
		if err != nil {
			return nil, fmt.Errorf("parsing base url: %w", err)
		}
		if !strings.HasSuffix(base.Path, "/") {
			base.Path += "/"
		}
		gh.BaseURL = base
	}

	return gh, nil
}

// splitRepository validates that repository is in owner/name form and returns
// its owner and name segments.
func splitRepository(repository string) (owner, name string, err error) {
	owner, name, ok := strings.Cut(repository, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", "", fmt.Errorf("repository must be in owner/name form, got %q", repository)
	}

	return owner, name, nil
}
