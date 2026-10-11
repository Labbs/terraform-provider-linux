package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/labbs/terraform-provider-linux/internal/provider"
)

// version is set by goreleaser.
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run the provider with support for debuggers like delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/labbs/linux",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err)
	}
}
