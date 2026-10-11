package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccHostDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheckLinux(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: `data "linux_host" "test" {}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.linux_host.test", "hostname"),
					resource.TestCheckResourceAttrSet("data.linux_host.test", "kernel_release"),
					resource.TestCheckResourceAttrSet("data.linux_host.test", "architecture"),
					resource.TestCheckResourceAttrSet("data.linux_host.test", "os_id"),
					resource.TestCheckResourceAttrSet("data.linux_host.test", "package_manager"),
					resource.TestCheckResourceAttrWith("data.linux_host.test", "cpu_count", nonZero),
					resource.TestCheckResourceAttrWith("data.linux_host.test", "memory_total", nonZero),
				),
			},
		},
	})
}

func nonZero(value string) error {
	if value == "" || value == "0" {
		return fmt.Errorf("expected a positive number, got %q", value)
	}
	return nil
}
