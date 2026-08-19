package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/UnstoppableMango/terraform-provider-git/internal/provider"
)

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "set to true to run the provider with support for debuggers like delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New, providerserver.ServeOpts{
		Address: "registry.opentofu.org/UnstoppableMango/git",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err)
	}
}
