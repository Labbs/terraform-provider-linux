package provider

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"golang.org/x/crypto/ssh"

	"github.com/labbs/terraform-provider-linux/internal/system"
)

func TestAccUserAndGroup(t *testing.T) {
	suffix := acctest.RandStringFromCharSet(6, acctest.CharSetAlpha)
	group, user := "tfg"+suffix, "tfu"+suffix
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	pub, _ := ssh.NewPublicKey(priv.Public())
	key := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub))) + " test@terraform"

	config := func(shell string, groups string) string {
		return fmt.Sprintf(`
resource "linux_group" "team" {
  name = %[1]q
  gid  = 4321
}

resource "linux_group" "existing" {
  name = "users"
}

resource "linux_user" "test" {
  name          = %[2]q
  uid           = 4321
  comment       = "Terraform test"
  shell         = %[3]q
  groups        = %[4]s
  password_hash = "$6$salt$hash"
  remove_home   = true
}

resource "linux_ssh_authorized_key" "test" {
  user = linux_user.test.name
  key  = %[5]q
}

data "linux_user" "test" {
  name = linux_user.test.name
}

data "linux_group" "team" {
  name       = linux_group.team.name
  depends_on = [linux_user.test]
}

data "linux_user" "missing" {
  name = "nobody${linux_group.team.gid}"
}
`, group, user, shell, groups, key)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheckRoot(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy: func(*terraform.State) error {
			c := testClient(t)
			if _, err := c.User(context.Background(), user); !system.IsNotExist(err) {
				return fmt.Errorf("user %s still exists (%v)", user, err)
			}
			if _, err := c.Group(context.Background(), group); !system.IsNotExist(err) {
				return fmt.Errorf("group %s still exists (%v)", group, err)
			}
			// The adopted group is left in place.
			if _, err := c.Group(context.Background(), "users"); err != nil {
				return fmt.Errorf("group users was deleted: %v", err)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config("/bin/sh", "[linux_group.team.name]"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("linux_user.test", "home", "/home/"+user),
					resource.TestCheckResourceAttr("linux_user.test", "group", user),
					resource.TestCheckResourceAttr("data.linux_user.test", "uid", "4321"),
					resource.TestCheckResourceAttr("data.linux_user.test", "shell", "/bin/sh"),
					resource.TestCheckTypeSetElemAttr("data.linux_user.test", "groups.*", group),
					resource.TestCheckResourceAttr("data.linux_group.team", "gid", "4321"),
					resource.TestCheckResourceAttr("data.linux_group.team", "members.0", user),
					resource.TestCheckResourceAttr("data.linux_user.missing", "exists", "false"),
					resource.TestCheckResourceAttr("linux_ssh_authorized_key.test", "path", "/home/"+user+"/.ssh/authorized_keys"),
					checkRemoteFile(t, "/home/"+user+"/.ssh/authorized_keys", key+"\n", "0600"),
				),
			},
			{
				Config: config("/bin/bash", "[]"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("linux_user.test", plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.linux_user.test", "shell", "/bin/bash"),
					resource.TestCheckResourceAttr("data.linux_user.test", "groups.#", "0"),
				),
			},
			{
				ResourceName:            "linux_user.test",
				ImportState:             true,
				ImportStateId:           user,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"password_hash", "remove_home", "groups"},
			},
		},
	})
}
