package provider

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccPatch(t *testing.T) {
	testAccPreCheckLinux(t)
	p := testPath("sshd_config")
	cleanupRemoteDir(t, p)
	writeSSHDConfig(t, p)

	config := fmt.Sprintf(`
resource "linux_patch" "test" {
  path  = %q
  patch = <<-EOT
    --- a/sshd_config
    +++ b/sshd_config
    @@ -1,4 +1,4 @@
     # managed by hand
     Port 22
    -PermitRootLogin yes
    +PermitRootLogin no
     UsePAM yes
  EOT
}
`, p)
	patched := strings.Replace(testSSHDConfig, "PermitRootLogin yes", "PermitRootLogin no", 1)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheckLinux(t) },
		ProtoV6ProviderFactories: testAccProviders,
		// Destroying reverts the patch.
		CheckDestroy: checkRemoteFile(t, p, testSSHDConfig, "0640"),
		Steps: []resource.TestStep{
			{
				Config: config,
				Check:  checkRemoteFile(t, p, patched, "0640"),
			},
			{
				// Reverting the change outside of Terraform makes it apply again.
				PreConfig:          func() { writeSSHDConfig(t, p) },
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: config,
				Check:  checkRemoteFile(t, p, patched, "0640"),
			},
		},
	})
}
