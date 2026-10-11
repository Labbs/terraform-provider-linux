package provider

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccSystemdUnit(t *testing.T) {
	unit := "tf-acc-" + acctest.RandString(8) + ".service"
	config := func(enabled, active bool, version string) string {
		return fmt.Sprintf(`
resource "linux_file" "unit" {
  path    = "/etc/systemd/system/%[1]s"
  content = <<-EOT
    [Unit]
    Description=Terraform acceptance test %[4]s

    [Service]
    ExecStart=/bin/sleep infinity

    [Install]
    WantedBy=multi-user.target
  EOT
}

resource "linux_systemd_unit" "test" {
  name              = %[1]q
  enabled           = %[2]t
  active            = %[3]t
  daemon_reload     = true
  revert_on_destroy = true
  triggers = {
    unit = linux_file.unit.sha256
  }
}

data "linux_systemd_unit" "test" {
  name = linux_systemd_unit.test.name
}
`, unit, enabled, active, version)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheckSystemd(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy: func(*terraform.State) error {
			status, err := testClient(t).UnitStatus(context.Background(), unit)
			if err != nil {
				return err
			}
			// revert_on_destroy stopped and disabled the unit.
			if status.Active() || status.Enabled() {
				return fmt.Errorf("%s is still active or enabled", unit)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config(true, true, "v1"),
				Check: resource.ComposeAggregateTestCheckFunc(
					checkUnit(t, unit, true, true),
					resource.TestCheckResourceAttr("data.linux_systemd_unit.test", "sub_state", "running"),
				),
			},
			{
				// Changing the unit file restarts the unit.
				Config: config(true, true, "v2"),
				Check:  checkUnit(t, unit, true, true),
			},
			{
				Config: config(false, false, "v2"),
				Check:  checkUnit(t, unit, false, false),
			},
			{
				ResourceName:            "linux_systemd_unit.test",
				ImportState:             true,
				ImportStateId:           unit,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"daemon_reload", "masked", "revert_on_destroy", "triggers"},
			},
		},
	})
}
