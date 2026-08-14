// Package gogit implements the git.Client interface using the pure-Go
// github.com/go-git/go-git/v5 library.
package gogit

import (
	"context"
	"fmt"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
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

	refs, err := remote.List(&git.ListOptions{Auth: authMethod(auth)})
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

func authMethod(auth providergit.Auth) transport.AuthMethod {
	if auth.Token == "" {
		return nil
	}

	return &http.BasicAuth{
		Username: "x-access-token",
		Password: auth.Token,
	}
}
