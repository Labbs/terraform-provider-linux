package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccFileDataSource(t *testing.T) {
	p := testPath("data.txt")
	cleanupRemoteDir(t, p)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheckLinux(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "linux_file" "source" {
  path    = %q
  content = "some data"
  mode    = "0600"
}

data "linux_file" "test" {
  path = linux_file.source.path
}
`, p),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.linux_file.test", "content", "some data"),
					resource.TestCheckResourceAttr("data.linux_file.test", "content_base64", "c29tZSBkYXRh"),
					resource.TestCheckResourceAttr("data.linux_file.test", "mode", "0600"),
					resource.TestCheckResourceAttrPair("data.linux_file.test", "sha256", "linux_file.source", "sha256"),
				),
			},
		},
	})
}
