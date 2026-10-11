package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccCommandDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: `
data "linux_command" "ok" {
  command     = "printf '%s' \"$NAME\"; echo warn >&2"
  environment = { NAME = "it's me" }
}

data "linux_command" "failing" {
  command       = "exit 3"
  allow_failure = true
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.linux_command.ok", "stdout", "it's me"),
					resource.TestCheckResourceAttr("data.linux_command.ok", "stderr", "warn\n"),
					resource.TestCheckResourceAttr("data.linux_command.ok", "exit_code", "0"),
					resource.TestCheckResourceAttr("data.linux_command.failing", "exit_code", "3"),
				),
			},
		},
	})
}
