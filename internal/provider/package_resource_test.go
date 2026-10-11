package provider

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/labbs/terraform-provider-linux/internal/system"
)

func TestAccPackage(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheckRoot(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy: func(*terraform.State) error {
			c := testClient(t)
			// sl was installed by Terraform, sudo was already there.
			if _, err := c.Package(context.Background(), "sl"); !system.IsNotExist(err) {
				return fmt.Errorf("sl is still installed (%v)", err)
			}
			if _, err := c.Package(context.Background(), "sudo"); err != nil {
				return fmt.Errorf("sudo was removed: %v", err)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: `
resource "linux_package" "sl" {
  name = "sl"
}

resource "linux_package" "sudo" {
  name = "sudo"
}

data "linux_package" "sl" {
  name = linux_package.sl.name
}

data "linux_package" "missing" {
  name = "cowsay"
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("linux_package.sl", "installed_version"),
					resource.TestCheckResourceAttr("data.linux_package.sl", "installed", "true"),
					resource.TestCheckResourceAttrPair("data.linux_package.sl", "version", "linux_package.sl", "installed_version"),
					resource.TestCheckResourceAttr("data.linux_package.missing", "installed", "false"),
					resource.TestCheckNoResourceAttr("data.linux_package.missing", "version"),
				),
			},
			{
				ResourceName:      "linux_package.sl",
				ImportState:       true,
				ImportStateId:     "sl",
				ImportStateVerify: true,
			},
		},
	})
}
