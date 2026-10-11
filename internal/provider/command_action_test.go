package provider

import (
	"context"
	"fmt"
	"path"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
)

func TestAccCommandAction(t *testing.T) {
	marker := testPath("action")
	t.Cleanup(func() {
		_, _ = testClient(t).Run(context.Background(), "rm -rf "+path.Dir(marker), nil)
	})
	resource.Test(t, resource.TestCase{
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_14_0)},
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
action "linux_command" "touch" {
  config {
    command     = "mkdir -p \"$(dirname \"$MARKER\")\" && touch \"$MARKER\""
    environment = { MARKER = %q }
  }
}

resource "terraform_data" "trigger" {
  input = "x"

  lifecycle {
    action_trigger {
      events  = [after_create]
      actions = [action.linux_command.touch]
    }
  }
}
`, marker),
				Check: checkRemoteExists(t, marker),
			},
		},
	})
}
