package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccSymlink(t *testing.T) {
	p := testPath("current")
	cleanupRemoteDir(t, p)
	config := func(target string) string {
		return fmt.Sprintf(`
resource "linux_symlink" "test" {
  path   = %q
  target = %q
}

data "linux_stat" "link" {
  path = linux_symlink.test.path
}

data "linux_stat" "target" {
  path   = linux_symlink.test.path
  follow = true
}

data "linux_stat" "missing" {
  path = "${linux_symlink.test.path}-missing"
}
`, p, target)
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheckLinux(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             checkRemoteMissing(t, p),
		Steps: []resource.TestStep{
			{
				Config: config("/etc"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.linux_stat.link", "exists", "true"),
					resource.TestCheckResourceAttr("data.linux_stat.link", "type", "symlink"),
					resource.TestCheckResourceAttr("data.linux_stat.link", "link_target", "/etc"),
					resource.TestCheckResourceAttr("data.linux_stat.target", "type", "directory"),
					resource.TestCheckResourceAttr("data.linux_stat.target", "owner", "root"),
					resource.TestCheckResourceAttr("data.linux_stat.missing", "exists", "false"),
					resource.TestCheckNoResourceAttr("data.linux_stat.missing", "type"),
				),
			},
			{
				Config: config("/etc/hostname"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.linux_stat.link", "link_target", "/etc/hostname"),
					resource.TestCheckResourceAttr("data.linux_stat.target", "type", "file"),
				),
			},
			{
				ResourceName:            "linux_symlink.test",
				ImportState:             true,
				ImportStateId:           p,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"create_parents"},
			},
		},
	})
}
