package provider

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
)

func TestAccSystemctlAction(t *testing.T) {
	unit := "tf-acc-" + acctest.RandString(8) + ".service"
	t.Cleanup(func() {
		c := testClient(t)
		_ = c.Systemctl(context.Background(), "stop", unit)
		_ = c.Remove(context.Background(), "/etc/systemd/system/"+unit)
	})
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheckSystemd(t) },
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_14_0)},
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: `
action "linux_systemctl" "missing_unit" {
  config {
    command = "restart"
  }
}
`,
				ExpectError: regexp.MustCompile(`restart requires a unit`),
			},
			{
				Config: fmt.Sprintf(`
action "linux_systemctl" "reload" {
  config {
    command = "daemon-reload"
  }
}

action "linux_systemctl" "start" {
  config {
    command = "start"
    unit    = %[1]q
  }
}

resource "linux_file" "unit" {
  path    = "/etc/systemd/system/%[1]s"
  content = "[Service]\nExecStart=/bin/sleep infinity\n"

  lifecycle {
    action_trigger {
      events  = [after_create]
      actions = [action.linux_systemctl.reload, action.linux_systemctl.start]
    }
  }
}
`, unit),
				Check: checkUnit(t, unit, false, true),
			},
		},
	})
}
