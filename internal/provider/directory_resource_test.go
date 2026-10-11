package provider

import (
	"context"
	"fmt"
	"path"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/labbs/terraform-provider-linux/internal/system"
)

func TestAccDirectory(t *testing.T) {
	p := testPath("a/b")
	cleanupRemoteDir(t, path.Dir(p))
	config := func(mode string) string {
		return fmt.Sprintf(`
resource "linux_directory" "test" {
  path          = %q
  mode          = %q
  force_destroy = true
}

resource "linux_file" "inside" {
  path    = "${linux_directory.test.path}/file"
  content = "x"
}
`, p, mode)
	}
	checkMode := func(mode string) resource.TestCheckFunc {
		return func(*terraform.State) error {
			info, err := testClient(t).Stat(context.Background(), p)
			if err != nil {
				return err
			}
			if info.Type != system.TypeDirectory || info.Mode != mode {
				return fmt.Errorf("%s: got %s %s", p, info.Type, info.Mode)
			}
			return nil
		}
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheckLinux(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             checkRemoteMissing(t, p),
		Steps: []resource.TestStep{
			{Config: config("0750"), Check: checkMode("0750")},
			{Config: config("0700"), Check: checkMode("0700")},
			{
				ResourceName:            "linux_directory.test",
				ImportState:             true,
				ImportStateId:           p,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"force_destroy"},
			},
		},
	})
}
