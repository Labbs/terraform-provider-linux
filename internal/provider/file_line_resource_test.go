package provider

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

func TestAccFileLine(t *testing.T) {
	testAccPreCheckLinux(t)
	p := testPath("sshd_config")
	cleanupRemoteDir(t, p)
	writeSSHDConfig(t, p)

	config := func(value string) string {
		return fmt.Sprintf(`
resource "linux_file_line" "root_login" {
  path  = %q
  line  = "PermitRootLogin %s"
  match = "^#?PermitRootLogin "
}

resource "linux_file_line" "banner" {
  path         = %q
  line         = "Banner /etc/issue.net"
  insert_after = "^Port "
}
`, p, value, p)
	}
	withLines := func(value string) string {
		return strings.Replace(
			strings.Replace(testSSHDConfig, "PermitRootLogin yes", "PermitRootLogin "+value, 1),
			"Port 22\n", "Port 22\nBanner /etc/issue.net\n", 1)
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProviders,
		// Destroying removes both lines.
		CheckDestroy: checkRemoteFile(t, p, strings.Replace(testSSHDConfig, "PermitRootLogin yes\n", "", 1), "0640"),
		Steps: []resource.TestStep{
			{
				Config: config("no"),
				Check:  checkRemoteFile(t, p, withLines("no"), "0640"),
			},
			{
				// The line is replaced in place.
				Config: config("prohibit-password"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("linux_file_line.root_login", plancheck.ResourceActionUpdate)},
				},
				Check: checkRemoteFile(t, p, withLines("prohibit-password"), "0640"),
			},
			{
				PreConfig:          func() { writeSSHDConfig(t, p) },
				Config:             config("prohibit-password"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: config("prohibit-password"),
				Check:  checkRemoteFile(t, p, withLines("prohibit-password"), "0640"),
			},
		},
	})
}

func TestAccFileLineCreate(t *testing.T) {
	p := testPath("environment")
	cleanupRemoteDir(t, p)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheckLinux(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "linux_file_line" "test" {
  path   = %q
  line   = "HTTP_PROXY=http://proxy:3128"
  create = true
}
`, p),
				Check: checkRemoteFile(t, p, "HTTP_PROXY=http://proxy:3128\n", "0644"),
			},
		},
	})
}
