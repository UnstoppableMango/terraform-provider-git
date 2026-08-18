package provider_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"

	gogithub "github.com/google/go-github/v75/github"
	"github.com/hashicorp/terraform-plugin-testing/config"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// These tests exercise the example configurations under examples/ directly,
// so the examples themselves are the single source of truth for both
// documentation and integration coverage.
//
// Most of the example .tf files deliberately omit explicit `provider "git"
// {}` blocks and a required_providers entry for git, and are loaded via
// ConfigDirectory: terraform-plugin-testing errors if a TestCase sets
// ProtoV6ProviderFactories and the step configuration also declares its own
// provider block for that name, and separately reattaches the in-process
// test provider under the default registry.terraform.io/hashicorp/<name>
// address, which a real source address (e.g. "UnstoppableMango/git") would
// not match. Real, published providers (github, random) don't have this
// constraint and are declared normally in examples/full/github/main.tf;
// ExternalProviders can't be combined with ConfigDirectory, so those are
// installed for real from the public registry during the test.
//
// examples/full/github/main.tf is the exception: it declares its own
// `provider "git" { git_implementation = "go-git" }` block, to demonstrate
// (and exercise, via its patch-stack resource) the go-git backend's
// go-gitdiff-based patch application explicitly, whether run as this
// acceptance test or as a standalone `terraform apply`. The
// ConfigDirectory/provider-block conflict described above only fires for
// ConfigDirectory, not a raw Config string, so TestAccExampleGitHubFull loads
// that file's content directly instead.

func TestAccExampleGitRepository(t *testing.T) {
	tfresource.Test(t, tfresource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []tfresource.TestStep{
			{
				ConfigDirectory: config.StaticDirectory("../../examples/data-sources/git_repository"),
			},
		},
	})
}

func TestAccExampleGitBranch(t *testing.T) {
	tfresource.Test(t, tfresource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []tfresource.TestStep{
			{
				ConfigDirectory: config.StaticDirectory("../../examples/resources/git_branch"),
			},
		},
	})
}

func TestAccExampleGitPatch(t *testing.T) {
	tfresource.Test(t, tfresource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []tfresource.TestStep{
			{
				ConfigDirectory: config.StaticDirectory("../../examples/data-sources/git_patch"),
			},
		},
	})
}

// TestAccExampleGitHubFull provisions and destroys a real, throwaway
// repository, under whichever GitHub account the provided GITHUB_TOKEN
// authenticates as, via the integrations/github provider, then tracks it
// with this provider. It
// requires a real GitHub token with repo-creation rights and is skipped
// unless GITHUB_TOKEN is set, on top of the TF_ACC gate every
// tfresource.Test already applies.
func TestAccExampleGitHubFull(t *testing.T) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		t.Skip("GITHUB_TOKEN not set; skipping example that provisions a real GitHub repository")
	}

	// The github provider (an external binary) reads its token from this
	// environment variable itself; the example config declares no explicit
	// provider "github" block (see the constraint noted on the other
	// TestAcc* tests in this file).
	t.Setenv("GITHUB_TOKEN", token)

	// Loaded via Config rather than ConfigDirectory: this example declares
	// its own `provider "git"` block (see the comment atop this file), which
	// ConfigDirectory can't tolerate alongside ProtoV6ProviderFactories.
	mainTf, err := os.ReadFile("../../examples/full/github/main.tf")
	if err != nil {
		t.Fatal(err)
	}

	tfresource.Test(t, tfresource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkGitHubRepoDestroyed(token),
		Steps: []tfresource.TestStep{
			{
				Config: string(mainTf),
				ConfigVariables: config.Variables{
					"github_token": config.StringVariable(token),
				},
			},
		},
	})
}

// checkGitHubRepoDestroyed confirms that no repository named
// terraform-provider-git-example-* remains under the GitHub account that
// GITHUB_TOKEN authenticates as, after the test run.
func checkGitHubRepoDestroyed(token string) tfresource.TestCheckFunc {
	return func(s *terraform.State) error {
		client := gogithub.NewClient(nil).WithAuthToken(token)

		user, _, err := client.Users.Get(context.Background(), "")
		if err != nil {
			return fmt.Errorf("looking up authenticated GitHub user: %w", err)
		}
		owner := user.GetLogin()

		for _, rs := range s.RootModule().Resources {
			if rs.Type != "github_repository" {
				continue
			}

			name := rs.Primary.Attributes["name"]
			_, resp, err := client.Repositories.Get(context.Background(), owner, name)
			if err == nil {
				return fmt.Errorf("repository %s still exists after destroy", name)
			}
			if resp == nil || resp.StatusCode != http.StatusNotFound {
				return fmt.Errorf("unexpected error checking repository %s was destroyed: %w", name, err)
			}
		}

		return nil
	}
}
