package provider

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccSed(t *testing.T) {
	testAccPreCheckLinux(t)
	p := testPath("sshd_config")
	cleanupRemoteDir(t, p)
	writeSSHDConfig(t, p)

	config := func(value string) string {
		return fmt.Sprintf(`
resource "linux_sed" "test" {
  path        = %q
  expressions = ["s/^#?PermitRootLogin .*/PermitRootLogin %s/"]
  extended    = true
}
`, p, value)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheckLinux(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: config("no"),
				Check:  checkRemoteFile(t, p, strings.Replace(testSSHDConfig, "PermitRootLogin yes", "PermitRootLogin no", 1), "0640"),
			},
			{
				Config: config("prohibit-password"),
				Check:  checkRemoteFile(t, p, strings.Replace(testSSHDConfig, "PermitRootLogin yes", "PermitRootLogin prohibit-password", 1), "0640"),
			},
			{
				PreConfig:          func() { writeSSHDConfig(t, p) },
				Config:             config("prohibit-password"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}
