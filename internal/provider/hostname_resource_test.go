package provider

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccHostname(t *testing.T) {
	name := "tf-acc-" + acctest.RandString(6)
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheckRoot(t)
			c := testClient(t)
			current, _, err := c.Hostname(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = c.SetHostname(context.Background(), current) })
		},
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "linux_hostname" "test" {
  name = %q
}

data "linux_host" "test" {
  depends_on = [linux_hostname.test]
}
`, name),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.linux_host.test", "hostname", name),
					checkRemoteFile(t, "/etc/hostname", name+"\n", "0644"),
				),
			},
		},
	})
}
