package gitlab

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-log/tflog"
	golab "gitlab.com/gitlab-org/api/client-go"
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

// WithoutRetries disables the underlying client's automatic retry-with-
// backoff behavior on server errors. Intended for tests against a fake
// server, where retries only add latency rather than resilience.
func WithoutRetries() Option {
	return func(c *client) {
		c.withoutRetries = true
	}
}

// New creates a Client that talks to the GitLab REST API via
// gitlab.com/gitlab-org/api/client-go.
func New(opts ...Option) Client {
	c := &client{}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

type client struct {
	baseURL        string
	withoutRetries bool
}

var _ Client = (*client)(nil)

func (c *client) ResolveMR(ctx context.Context, project string, mr int64, token string) (Resolution, error) {
	gl, err := c.newGitLab(token)
	if err != nil {
		return Resolution{}, err
	}

	tflog.Debug(ctx, "resolving merge request", map[string]any{"project": project, "mr": mr})

	mrObj, _, err := gl.MergeRequests.GetMergeRequest(project, mr, nil, golab.WithContext(ctx))
	if err != nil {
		return Resolution{}, fmt.Errorf("resolving merge request: %w", err)
	}

	sha := mrObj.SHA
	if sha == "" {
		return Resolution{}, fmt.Errorf("merge request response missing head commit sha")
	}

	tflog.Debug(ctx, "fetching merge request diff", map[string]any{"project": project, "mr": mr, "resolved_sha": sha})

	var files []fileDiff
	opt := &golab.ListMergeRequestDiffsOptions{ListOptions: golab.ListOptions{PerPage: 100}}
	for {
		diffs, resp, err := gl.MergeRequests.ListMergeRequestDiffs(project, mr, opt, golab.WithContext(ctx))
		if err != nil {
			return Resolution{}, fmt.Errorf("fetching diff: %w", err)
		}
		for _, d := range diffs {
			files = append(files, fileDiff{
				oldPath:     d.OldPath,
				newPath:     d.NewPath,
				aMode:       d.AMode,
				bMode:       d.BMode,
				newFile:     d.NewFile,
				deletedFile: d.DeletedFile,
				renamedFile: d.RenamedFile,
				diff:        d.Diff,
			})
		}
		tflog.Debug(ctx, "fetched diff page", map[string]any{"project": project, "mr": mr, "page": resp.CurrentPage, "file_count": len(diffs)})
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}

	diff := renderUnifiedDiff(files)
	tflog.Debug(ctx, "resolved merge request", map[string]any{"project": project, "mr": mr, "resolved_sha": sha, "file_count": len(files), "diff_bytes": len(diff)})

	return Resolution{SHA: sha, Diff: diff}, nil
}

func (c *client) ResolveCommit(ctx context.Context, project, sha, token string) (Resolution, error) {
	gl, err := c.newGitLab(token)
	if err != nil {
		return Resolution{}, err
	}

	tflog.Debug(ctx, "fetching commit diff", map[string]any{"project": project, "sha": sha})

	var files []fileDiff
	opt := &golab.GetCommitDiffOptions{ListOptions: golab.ListOptions{PerPage: 100}}
	for {
		diffs, resp, err := gl.Commits.GetCommitDiff(project, sha, opt, golab.WithContext(ctx))
		if err != nil {
			return Resolution{}, fmt.Errorf("fetching diff: %w", err)
		}
		for _, d := range diffs {
			files = append(files, fileDiff{
				oldPath:     d.OldPath,
				newPath:     d.NewPath,
				aMode:       d.AMode,
				bMode:       d.BMode,
				newFile:     d.NewFile,
				deletedFile: d.DeletedFile,
				renamedFile: d.RenamedFile,
				diff:        d.Diff,
			})
		}
		tflog.Debug(ctx, "fetched diff page", map[string]any{"project": project, "sha": sha, "page": resp.CurrentPage, "file_count": len(diffs)})
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}

	diff := renderUnifiedDiff(files)
	tflog.Debug(ctx, "resolved commit", map[string]any{"project": project, "sha": sha, "file_count": len(files), "diff_bytes": len(diff)})

	return Resolution{SHA: sha, Diff: diff}, nil
}

// newGitLab builds a gitlab.Client, applying the optional base URL
// override. Auth uses GitLab's PRIVATE-TOKEN convention (personal/project
// access tokens); an empty token still produces a valid, effectively
// unauthenticated client, since the library only sets the header when
// asked and GitLab treats an empty PRIVATE-TOKEN as no token.
func (c *client) newGitLab(token string) (*golab.Client, error) {
	var opts []golab.ClientOptionFunc
	if c.baseURL != "" {
		opts = append(opts, golab.WithBaseURL(c.baseURL))
	}
	if c.withoutRetries {
		opts = append(opts, golab.WithoutRetries())
	}

	gl, err := golab.NewClient(token, opts...)
	if err != nil {
		return nil, fmt.Errorf("creating gitlab client: %w", err)
	}

	return gl, nil
}

// fileDiff is a common shape adapted from GitLab's per-file diff objects
// (golab.Diff from commit diffs, golab.MergeRequestDiff from merge request
// diffs), which carry the same fields under different struct types.
type fileDiff struct {
	oldPath, newPath                  string
	aMode, bMode                      string
	newFile, deletedFile, renamedFile bool
	diff                              string
}

// renderUnifiedDiff synthesizes a full unified diff from GitLab's per-file
// diff objects. GitLab's REST API, unlike GitHub's, has no endpoint that
// returns a ready-to-use unified diff directly: each file's diff field only
// contains the "@@ ...@@" hunk body, not the "diff --git"/"---"/"+++"
// header lines a unified diff parser (e.g. go-gitdiff, used by this
// provider's patch-apply path) needs to locate file boundaries. This
// targets the common case of added/modified/deleted text files; renamed
// files get best-effort "rename from"/"rename to" headers, and binary
// files (whose diff field GitLab typically leaves empty) are not specially
// handled.
func renderUnifiedDiff(files []fileDiff) string {
	var b strings.Builder

	for _, f := range files {
		// old_path/new_path are usually identical except on a rename, but
		// GitLab's docs only show a worked example for plain modifications,
		// so fall back to the other side's path when one is empty (e.g. a
		// hypothetical add/delete response using "" for the nonexistent
		// side) rather than assuming they're always both populated.
		aName, bName := f.oldPath, f.newPath
		if aName == "" {
			aName = bName
		}
		if bName == "" {
			bName = aName
		}
		fmt.Fprintf(&b, "diff --git a/%s b/%s\n", aName, bName)

		switch {
		case f.newFile:
			mode := f.bMode
			if mode == "" {
				mode = "100644"
			}
			fmt.Fprintf(&b, "new file mode %s\n", mode)
		case f.deletedFile:
			mode := f.aMode
			if mode == "" {
				mode = "100644"
			}
			fmt.Fprintf(&b, "deleted file mode %s\n", mode)
		case f.renamedFile && f.oldPath != f.newPath:
			fmt.Fprintf(&b, "rename from %s\nrename to %s\n", f.oldPath, f.newPath)
		}

		a := "a/" + f.oldPath
		if f.newFile {
			a = "/dev/null"
		}
		newB := "b/" + f.newPath
		if f.deletedFile {
			newB = "/dev/null"
		}
		fmt.Fprintf(&b, "--- %s\n+++ %s\n", a, newB)

		b.WriteString(f.diff)
		if !strings.HasSuffix(f.diff, "\n") {
			b.WriteString("\n")
		}
	}

	return b.String()
}
