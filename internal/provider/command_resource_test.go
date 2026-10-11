package provider

import (
	"context"
	"fmt"
	"path"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

func TestAccCommand(t *testing.T) {
	marker := testPath("marker")
	t.Cleanup(func() {
		_, _ = testClient(t).Run(context.Background(), "rm -rf "+path.Dir(marker), nil)
	})
	config := func(trigger string) string {
		return fmt.Sprintf(`
resource "linux_command" "test" {
  create = "mkdir -p \"$(dirname \"$MARKER\")\" && echo created > \"$MARKER\""
  read   = "cat \"$MARKER\""
  update = "echo \"updated $VERSION\" > \"$MARKER\""
  delete = "rm -f \"$MARKER\""

  environment = {
    MARKER  = %q
    VERSION = %q
  }
}
`, marker, trigger)
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             checkRemoteMissing(t, marker),
		Steps: []resource.TestStep{
			{
				Config: config("1"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("linux_command.test", "output", "created\n"),
					resource.TestCheckResourceAttrSet("linux_command.test", "id"),
				),
			},
			{
				Config: config("2"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("linux_command.test", plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.TestCheckResourceAttr("linux_command.test", "output", "updated 2\n"),
			},
			{
				// The read script fails once the marker is gone: the
				// resource is created again.
				PreConfig: func() {
					if _, err := testClient(t).Run(context.Background(), "rm -f "+marker, nil); err != nil {
						t.Fatal(err)
					}
				},
				Config: config("2"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("linux_command.test", plancheck.ResourceActionCreate),
					},
				},
				Check: resource.TestCheckResourceAttr("linux_command.test", "output", "created\n"),
			},
		},
	})
}

func TestAccCommandReplace(t *testing.T) {
	config := func(trigger string) string {
		return fmt.Sprintf(`
resource "linux_command" "test" {
  create   = "echo $VALUE"
  triggers = { value = %q }
  environment = { VALUE = %q }
}
`, trigger, trigger)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: config("a"),
				Check:  resource.TestCheckResourceAttr("linux_command.test", "output", "a\n"),
			},
			{
				Config: config("b"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("linux_command.test", plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
				Check: resource.TestCheckResourceAttr("linux_command.test", "output", "b\n"),
			},
		},
	})
}
