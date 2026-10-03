package main

import (
	"context"
	"log"

	"github.com/TrogonStack/terraform-provider-anthropic/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
)

var version string = "dev"

func main() {
	err := providerserver.Serve(
		context.Background(),
		provider.New(version),
		providerserver.ServeOpts{
			Address: "registry.terraform.io/trogonstack/anthropic",
		},
	)
	if err != nil {
		log.Fatal(err)
	}
}
