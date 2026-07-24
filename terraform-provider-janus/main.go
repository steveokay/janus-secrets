// Command terraform-provider-janus is the Terraform provider plugin binary for
// the Janus secrets manager. Terraform launches it over the plugin protocol; it
// is not meant to be run directly.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/steveokay/janus-secrets/terraform-provider-janus/internal/provider"
)

// version is set via -ldflags at release time (goreleaser); "dev" otherwise.
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "set to run the provider with support for debuggers like delve")
	flag.Parse()

	opts := providerserver.ServeOpts{
		// Address under which Terraform discovers the provider in the registry.
		Address: "registry.terraform.io/steveokay/janus",
		Debug:   debug,
	}

	if err := providerserver.Serve(context.Background(), provider.New(version), opts); err != nil {
		log.Fatal(err.Error())
	}
}
