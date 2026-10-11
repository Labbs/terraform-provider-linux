package provider

import (
	"context"
	"fmt"
	"path"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/labbs/terraform-provider-linux/internal/system"
)

func TestAccFile(t *testing.T) {
	p := testPath("conf/app.conf")
	cleanupRemoteDir(t, path.Dir(p))
	config := func(content, mode string) string {
		return fmt.Sprintf(`
resource "linux_file" "test" {
  path    = %q
  content = %q
  mode    = %q
}
`, p, content, mode)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheckLinux(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             checkRemoteMissing(t, p),
		Steps: []resource.TestStep{
			{
				Config: config("hello\n", "640"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("linux_file.test", "id", p),
					resource.TestCheckResourceAttr("linux_file.test", "mode", "640"),
					resource.TestCheckResourceAttr("linux_file.test", "sha256", "5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03"),
					resource.TestCheckResourceAttrSet("linux_file.test", "owner"),
					resource.TestCheckResourceAttrSet("linux_file.test", "group"),
					checkRemoteFile(t, p, "hello\n", "0640"),
				),
			},
			{
				Config: config("hello again\n", "0600"),
				Check:  checkRemoteFile(t, p, "hello again\n", "0600"),
			},
			{
				// A change made outside of Terraform shows in the plan.
				PreConfig: func() {
					err := testClient(t).WriteFile(context.Background(), p, strings.NewReader("tampered\n"), system.Attributes{Mode: "0600"}, false)
					if err != nil {
						t.Fatal(err)
					}
				},
				Config:             config("hello again\n", "0600"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: config("hello again\n", "0600"),
				Check:  checkRemoteFile(t, p, "hello again\n", "0600"),
			},
			{
				ResourceName:            "linux_file.test",
				ImportState:             true,
				ImportStateId:           p,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"create_parents"},
			},
		},
	})
}

func TestAccFileBase64(t *testing.T) {
	p := testPath("blob.bin")
	cleanupRemoteDir(t, p)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheckLinux(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             checkRemoteMissing(t, p),
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "linux_file" "test" {
  path           = %q
  content_base64 = "AAEC/w=="
}
`, p),
				Check: checkRemoteFile(t, p, "\x00\x01\x02\xff", "0644"),
			},
		},
	})
}
