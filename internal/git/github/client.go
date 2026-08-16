package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// defaultBaseURL is the real GitHub REST API base URL used unless overridden
// via WithBaseURL.
const defaultBaseURL = "https://api.github.com"

// diffAccept is the Accept header value that makes the GitHub commits
// endpoint return a raw unified diff instead of a JSON commit object.
const diffAccept = "application/vnd.github.v3.diff"

// Option configures a client constructed via New.
type Option func(*client)

// WithBaseURL overrides the API base URL. Intended for tests to point the
// client at a fake server.
func WithBaseURL(url string) Option {
	return func(c *client) {
		c.baseURL = url
	}
}

// New creates a Client that talks to the GitHub REST API.
func New(opts ...Option) Client {
	c := &client{
		baseURL:    defaultBaseURL,
		httpClient: http.DefaultClient,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

type client struct {
	baseURL    string
	httpClient *http.Client
}

var _ Client = (*client)(nil)

type pullRequestResponse struct {
	Head struct {
		SHA string `json:"sha"`
	} `json:"head"`
}

func (c *client) ResolvePR(ctx context.Context, repository string, pr int64, auth Auth) (Resolution, error) {
	repoPath, err := escapeRepository(repository)
	if err != nil {
		return Resolution{}, err
	}

	reqURL := fmt.Sprintf("%s/repos/%s/pulls/%d", c.baseURL, repoPath, pr)

	body, err := c.get(ctx, reqURL, "", auth)
	if err != nil {
		return Resolution{}, fmt.Errorf("resolving pull request: %w", err)
	}

	var pull pullRequestResponse
	if err := json.Unmarshal(body, &pull); err != nil {
		return Resolution{}, fmt.Errorf("parsing pull request response: %w", err)
	}

	if pull.Head.SHA == "" {
		return Resolution{}, fmt.Errorf("pull request response missing head commit sha")
	}

	return c.ResolveCommit(ctx, repository, pull.Head.SHA, auth)
}

func (c *client) ResolveCommit(ctx context.Context, repository string, sha string, auth Auth) (Resolution, error) {
	repoPath, err := escapeRepository(repository)
	if err != nil {
		return Resolution{}, err
	}

	reqURL := fmt.Sprintf("%s/repos/%s/commits/%s", c.baseURL, repoPath, url.PathEscape(sha))

	diff, err := c.get(ctx, reqURL, diffAccept, auth)
	if err != nil {
		return Resolution{}, fmt.Errorf("fetching diff: %w", err)
	}

	return Resolution{SHA: sha, Diff: string(diff)}, nil
}

// escapeRepository validates that repository is in owner/name form and
// returns it with each segment percent-escaped for safe use in a URL path.
func escapeRepository(repository string) (string, error) {
	owner, name, ok := strings.Cut(repository, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", fmt.Errorf("repository must be in owner/name form, got %q", repository)
	}

	return url.PathEscape(owner) + "/" + url.PathEscape(name), nil
}

// get issues a GET request to url, optionally setting an Accept header, and
// returns the raw response body. It returns an error whose message includes
// the response status code for any non-2xx response.
func (c *client) get(ctx context.Context, url string, accept string, auth Auth) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	if accept != "" {
		req.Header.Set("Accept", accept)
	}

	if auth.Token != "" {
		req.Header.Set("Authorization", "Bearer "+auth.Token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected response status %d from %s: %s", resp.StatusCode, url, string(body))
	}

	return body, nil
}
