package main

import (
	"github.com/hashicorp/terraform-plugin-sdk/v2/plugin"
	"github.com/labbs/terraform-provider-linux/pkg"
)

func main() {
	plugin.Serve(&plugin.ServeOpts{
		ProviderFunc: pkg.Provider,
	})
}
