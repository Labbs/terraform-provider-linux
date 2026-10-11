//go:build generate

package tools

import (
	_ "github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs"
)

// Format Terraform code for use in documentation.
//go:generate terraform fmt -recursive ../examples/

// Generate documentation. The Terraform version is pinned so that the output
// does not depend on the local installation: actions are only documented
// with Terraform 1.14 or later.
//go:generate go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs generate --provider-dir .. -provider-name linux --tf-version 1.16.5
