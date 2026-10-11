package provider

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

func TestAccFileBlock(t *testing.T) {
	testAccPreCheckLinux(t)
	p := testPath("sshd_config")
	cleanupRemoteDir(t, p)
	writeSSHDConfig(t, p)

	config := func(content string) string {
		return fmt.Sprintf(`
resource "linux_file_block" "test" {
  path         = %q
  marker       = "# {mark} match deploy"
  insert_after = "^UsePAM"
  content      = %q
}
`, p, content)
	}
	withBlock := func(content string) string {
		return testSSHDConfig + "# BEGIN match deploy\n" + content + "# END match deploy\n"
	}
	const block = "Match User deploy\n    PasswordAuthentication no\n"

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             checkRemoteFile(t, p, testSSHDConfig, "0640"),
		Steps: []resource.TestStep{
			{
				Config: config(block),
				Check:  checkRemoteFile(t, p, withBlock(block), "0640"),
			},
			{
				Config: config(block + "    X11Forwarding no\n"),
				Check:  checkRemoteFile(t, p, withBlock(block+"    X11Forwarding no\n"), "0640"),
			},
			{
				// An edit inside the block shows up as an update.
				PreConfig: func() {
					_, err := testClient(t).EditFile(context.Background(), p, nil, func(b []byte) ([]byte, error) {
						return []byte(strings.Replace(string(b), "X11Forwarding no", "X11Forwarding yes", 1)), nil
					})
					if err != nil {
						t.Fatal(err)
					}
				},
				Config: config(block + "    X11Forwarding no\n"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("linux_file_block.test", plancheck.ResourceActionUpdate)},
				},
				Check: checkRemoteFile(t, p, withBlock(block+"    X11Forwarding no\n"), "0640"),
			},
		},
	})
}
