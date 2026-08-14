package main

import (
	"context"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/UnstoppableMango/terraform-provider-git/internal/provider"
)

func main() {
	err := providerserver.Serve(context.Background(), provider.New, providerserver.ServeOpts{
		Address: "registry.terraform.io/UnstoppableMango/git",
	})
	if err != nil {
		log.Fatal(err)
	}
}
