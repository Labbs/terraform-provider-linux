package provider

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccSedDataSource(t *testing.T) {
	testAccPreCheckLinux(t)
	p := testPath("sshd_config")
	cleanupRemoteDir(t, p)
	writeSSHDConfig(t, p)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
data "linux_sed" "change" {
  path        = %[1]q
  expressions = ["s/^PermitRootLogin .*/PermitRootLogin no/"]
}

data "linux_sed" "noop" {
  path        = %[1]q
  expressions = ["s/^Port .*/Port 22/"]
}
`, p),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.linux_sed.change", "pending", "true"),
					resource.TestCheckResourceAttr("data.linux_sed.change", "result",
						strings.Replace(testSSHDConfig, "PermitRootLogin yes", "PermitRootLogin no", 1)),
					resource.TestCheckResourceAttr("data.linux_sed.noop", "pending", "false"),
					// The file itself is left untouched.
					checkRemoteFile(t, p, testSSHDConfig, "0640"),
				),
			},
		},
	})
}
