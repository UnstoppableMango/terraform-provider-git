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
// so the examples are the single source of truth for docs and integration
// coverage. Most examples omit an explicit `provider "git" {}` block and are
// loaded via ConfigDirectory, since ProtoV6ProviderFactories conflicts with a
// step declaring its own provider block; examples/full/github/main.tf is the
// exception and is loaded via Config instead (see TestAccExampleGitHubFull).

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
// this provider. Skipped unless GITHUB_TOKEN is set.
func TestAccExampleGitHubFull(t *testing.T) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		t.Skip("GITHUB_TOKEN not set; skipping example that provisions a real GitHub repository")
	}

	// The github provider reads its token from this environment variable itself.
	t.Setenv("GITHUB_TOKEN", token)

	// Loaded via Config, not ConfigDirectory, since this example declares its own provider "git" block.
	mainTf, err := os.ReadFile("../../examples/full/github/main.tf")
	if err != nil {
		t.Fatal(err)
	}

	tfresource.Test(t, tfresource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		// main.tf also declares the github and random providers directly;
		// ExternalProviders tells the test framework to download and lock
		// them from the registry during init, since they aren't part of
		// ProtoV6ProviderFactories above.
		ExternalProviders: map[string]tfresource.ExternalProvider{
			"github": {Source: "integrations/github", VersionConstraint: ">= 6.0.0"},
			"random": {Source: "hashicorp/random", VersionConstraint: ">= 3.0.0"},
		},
		CheckDestroy: checkGitHubRepoDestroyed(token),
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
