package provider

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccSysctl(t *testing.T) {
	// A per-network-namespace parameter, so that a container does not
	// change its host.
	const param = "net.ipv4.ip_local_port_range"
	file := "/etc/sysctl.d/99-tf-acc-" + acctest.RandString(6) + ".conf"
	var before string
	config := func(value string) string {
		return fmt.Sprintf(`
resource "linux_sysctl" "test" {
  name  = %q
  value = %q
  file  = %q
}
`, param, value, file)
	}
	checkValue := func(value string) resource.TestCheckFunc {
		return resource.ComposeAggregateTestCheckFunc(
			func(*terraform.State) error {
				current, err := testClient(t).Sysctl(context.Background(), param)
				if err == nil && current != value {
					err = fmt.Errorf("%s = %q, want %q", param, current, value)
				}
				return err
			},
			checkRemoteFile(t, file, param+" = "+value+"\n", "0644"),
		)
	}

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheckRoot(t)
			c := testClient(t)
			var err error
			if before, err = c.Sysctl(context.Background(), param); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = c.SetSysctl(context.Background(), param, before)
				_ = c.Remove(context.Background(), file)
			})
		},
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             checkRemoteFile(t, file, "", "0644"),
		Steps: []resource.TestStep{
			{Config: config("20000  40000"), Check: checkValue("20000 40000")},
			{Config: config("21000 41000"), Check: checkValue("21000 41000")},
			{
				// A change of the current value shows up as an update.
				PreConfig: func() {
					if err := testClient(t).SetSysctl(context.Background(), param, "22000 42000"); err != nil {
						t.Fatal(err)
					}
				},
				Config: config("21000 41000"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("linux_sysctl.test", plancheck.ResourceActionUpdate)},
				},
				Check: checkValue("21000 41000"),
			},
		},
	})
}
