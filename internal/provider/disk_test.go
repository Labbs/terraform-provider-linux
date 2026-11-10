package provider

import (
	"context"
	"fmt"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// testAccPreCheckDisk skips tests that create loop devices, partition and
// format them, and mount file systems: they need root on a host that
// creates device nodes for partitions, such as a CI runner.
func testAccPreCheckDisk(t *testing.T) {
	t.Helper()
	testAccPreCheckLinux(t)
	if os.Getenv("LINUX_ACC_DISK") == "" {
		t.Skip("creates loop devices and mounts file systems: set LINUX_ACC_DISK=1 on a throwaway host")
	}
}

// remoteRun runs a script on the target, failing the test on errors.
func remoteRun(t *testing.T, script string) string {
	t.Helper()
	res, err := testClient(t).Run(context.Background(), "set -e\n"+script, nil)
	if err != nil {
		t.Fatalf("running %q: %v", script, err)
	}
	return strings.TrimSpace(string(res.Stdout))
}

// testLoopDevice creates a loop device backed by a sparse file of size
// megabytes, with partition scanning, and returns its path.
func testLoopDevice(t *testing.T, dir string, size int) string {
	t.Helper()
	file := dir + "/disk-" + strconv.Itoa(size) + ".img"
	dev := remoteRun(t, fmt.Sprintf("mkdir -p %[1]s\ntruncate -s %[2]dM %[3]s\nlosetup -f -P --show %[3]s", dir, size, file))
	t.Cleanup(func() {
		_, _ = testClient(t).Run(context.Background(), fmt.Sprintf(
			"umount %[1]sp* %[1]s 2>/dev/null; swapoff %[1]sp* %[1]s 2>/dev/null; losetup -d %[1]s; rm -f %[2]s", dev, file), nil)
	})
	return dev
}

// errorPattern matches message in an error that Terraform may have wrapped
// over several lines.
func errorPattern(message string) *regexp.Regexp {
	return regexp.MustCompile(strings.ReplaceAll(regexp.QuoteMeta(message), " ", `\s+`))
}

func checkRemoteScript(t *testing.T, script, want string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		res, err := testClient(t).Run(context.Background(), script, nil)
		if err != nil {
			return fmt.Errorf("running %q: %w", script, err)
		}
		if got := strings.TrimSpace(string(res.Stdout)); got != want {
			return fmt.Errorf("%q printed %q, want %q", script, got, want)
		}
		return nil
	}
}

func TestAccDisk(t *testing.T) {
	dir := path.Dir(testPath("x"))
	testAccPreCheckDisk(t)
	cleanupRemoteDir(t, dir+"/x")
	dev := testLoopDevice(t, dir, 96)
	mnt := dir + "/mnt"
	fstab := dir + "/fstab"

	config := func(size, options string) string {
		sizeAttr := ""
		if size != "" {
			sizeAttr = fmt.Sprintf("size = %q", size)
		}
		return fmt.Sprintf(`
resource "linux_partition" "data" {
  device = %[1]q
  number = 1
  name   = "data"
  %[2]s
}

resource "linux_filesystem" "data" {
  device = linux_partition.data.path
  type   = "ext4"
  label  = "tfacc"
  grow   = true

  triggers = {
    size = linux_partition.data.size_bytes
  }
}

resource "linux_mount" "data" {
  path    = %[3]q
  device  = "UUID=${linux_filesystem.data.uuid}"
  fstype  = "ext4"
  options = %[4]q
  pass    = 2
  fstab   = %[5]q
}
`, dev, sizeAttr, mnt, options, fstab)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheckDisk(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy: func(s *terraform.State) error {
			// Unmounted and out of fstab, but the partition and its data
			// are kept.
			if m, err := testClient(t).Mounted(context.Background(), mnt); err != nil || m != nil {
				return fmt.Errorf("still mounted: %v, %v", m, err)
			}
			if content, _ := testClient(t).Fstab(context.Background(), fstab); strings.Contains(content, mnt) {
				return fmt.Errorf("fstab still lists %s:\n%s", mnt, content)
			}
			return checkRemoteScript(t, "blkid -p -o value -s LABEL "+dev+"p1", "tfacc")(s)
		},
		Steps: []resource.TestStep{
			{
				Config: config("32M", "defaults,noatime"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith("linux_partition.data", "path", func(v string) error {
						if v != dev+"p1" {
							return fmt.Errorf("path = %s, want %sp1", v, dev)
						}
						return nil
					}),
					resource.TestCheckResourceAttr("linux_partition.data", "size_bytes", strconv.Itoa(32<<20)),
					resource.TestCheckResourceAttr("linux_partition.data", "type", "linux"),
					resource.TestCheckResourceAttrSet("linux_partition.data", "uuid"),
					resource.TestCheckResourceAttrSet("linux_filesystem.data", "uuid"),
					resource.TestCheckResourceAttr("linux_filesystem.data", "label", "tfacc"),
					resource.TestCheckResourceAttr("linux_mount.data", "mounted", "true"),
					checkRemoteScript(t, "findmnt -n -o OPTIONS "+mnt+" | tr , '\\n' | grep -x noatime", "noatime"),
					func(s *terraform.State) error {
						// Data written now must survive the next steps.
						remoteRun(t, "echo precious > "+mnt+"/marker")
						uuid := s.RootModule().Resources["linux_filesystem.data"].Primary.Attributes["uuid"]
						return checkRemoteScript(t, "cat "+fstab, fmt.Sprintf("UUID=%s %s ext4 defaults,noatime 0 2", uuid, mnt))(s)
					},
				),
			},
			{
				// Without a size, the partition grows into the free space,
				// and the file system with it, in the same apply.
				Config: config("", "defaults,noatime"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("linux_partition.data", plancheck.ResourceActionUpdate),
						plancheck.ExpectResourceAction("linux_filesystem.data", plancheck.ResourceActionUpdate),
						plancheck.ExpectResourceAction("linux_mount.data", plancheck.ResourceActionNoop),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith("linux_partition.data", "size_bytes", func(v string) error {
						if n, _ := strconv.Atoi(v); n < 90<<20 {
							return fmt.Errorf("size_bytes = %s, expected the partition to fill the disk", v)
						}
						return nil
					}),
					checkRemoteScript(t, "cat "+mnt+"/marker", "precious"),
					checkRemoteScript(t, "test $(df -k --output=size "+mnt+" | tail -n 1) -gt 60000 && echo grown", "grown"),
				),
			},
			{
				// New options remount the file system.
				Config: config("", "defaults,nodev"),
				Check: resource.ComposeAggregateTestCheckFunc(
					checkRemoteScript(t, "findmnt -n -o OPTIONS "+mnt+" | tr , '\\n' | grep -x nodev", "nodev"),
					checkRemoteScript(t, "cat "+mnt+"/marker", "precious"),
				),
			},
			{
				// Unmounted outside of Terraform: mounted again.
				PreConfig:          func() { remoteRun(t, "umount "+mnt) },
				Config:             config("", "defaults,nodev"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: config("", "defaults,nodev"),
				Check:  checkRemoteScript(t, "cat "+mnt+"/marker", "precious"),
			},
		},
	})
}

// A device that already holds data is used as it is, never formatted.
func TestAccDiskExistingData(t *testing.T) {
	dir := path.Dir(testPath("x"))
	testAccPreCheckDisk(t)
	cleanupRemoteDir(t, dir+"/x")
	dev := testLoopDevice(t, dir, 64)
	mnt := dir + "/mnt"
	// A disk formatted without partition table, with data on it.
	uuid := remoteRun(t, fmt.Sprintf(`mkfs.ext4 -q -F -L before %[1]s
mkdir -p %[2]s
mount %[1]s %[2]s
echo precious > %[2]s/marker
umount %[2]s
blkid -p -o value -s UUID %[1]s`, dev, mnt))
	t.Cleanup(func() { _, _ = testClient(t).Run(context.Background(), "umount "+mnt+" 2>/dev/null", nil) })

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheckDisk(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				// Partitioning it would lose the data.
				Config: fmt.Sprintf(`
resource "linux_partition" "test" {
  device = %q
  number = 1
}
`, dev),
				ExpectError: errorPattern(`refusing to partition it`),
			},
			{
				// Another file system type is refused.
				Config: fmt.Sprintf(`
resource "linux_filesystem" "test" {
  device = %q
  type   = "swap"
}
`, dev),
				ExpectError: errorPattern(`already holds a ext4 file system labelled "before"`),
			},
			{
				// The same type is used as it is, and only relabelled.
				Config: fmt.Sprintf(`
resource "linux_filesystem" "test" {
  device = %[1]q
  type   = "ext4"
  label  = "after"
}

resource "linux_mount" "test" {
  path    = %[2]q
  device  = "UUID=${linux_filesystem.test.uuid}"
  fstype  = "ext4"
  persist = false
}
`, dev, mnt),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith("linux_filesystem.test", "uuid", func(v string) error {
						if v != uuid {
							return fmt.Errorf("uuid = %s, want %s: the device was formatted", v, uuid)
						}
						return nil
					}),
					resource.TestCheckResourceAttr("linux_filesystem.test", "label", "after"),
					checkRemoteScript(t, "cat "+mnt+"/marker", "precious"),
				),
			},
		},
	})
}

// A mount that existed before the resource is left in place on destroy.
func TestAccMountAdopted(t *testing.T) {
	testAccPreCheckDisk(t)
	dir := path.Dir(testPath("x"))
	cleanupRemoteDir(t, dir+"/x")
	mnt := dir + "/tmp"
	fstab := dir + "/fstab"
	t.Cleanup(func() { _, _ = testClient(t).Run(context.Background(), "umount "+mnt+" 2>/dev/null", nil) })

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheckDisk(t)
			remoteRun(t, fmt.Sprintf("mkdir -p %[1]s\nmount -t tmpfs -o size=1m tmpfs %[1]s\necho '# comment' > %[2]s", mnt, fstab))
		},
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy: resource.ComposeTestCheckFunc(
			checkRemoteScript(t, "findmnt -n -o FSTYPE "+mnt, "tmpfs"),
			checkRemoteScript(t, "cat "+fstab, "# comment\ntmpfs "+mnt+" tmpfs size=2m 0 0"),
		),
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "linux_mount" "test" {
  path    = %q
  device  = "tmpfs"
  fstype  = "tmpfs"
  options = "size=2m"
  fstab   = %q
}
`, mnt, fstab),
				Check: checkRemoteScript(t, "cat "+fstab, "# comment\ntmpfs "+mnt+" tmpfs size=2m 0 0"),
			},
		},
	})
}

func TestAccSwap(t *testing.T) {
	dir := path.Dir(testPath("x"))
	testAccPreCheckDisk(t)
	cleanupRemoteDir(t, dir+"/x")
	dev := testLoopDevice(t, dir, 32)
	fstab := dir + "/fstab"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheckDisk(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy: func(s *terraform.State) error {
			active, err := testClient(t).SwapActive(context.Background(), dev)
			if err != nil || active {
				return fmt.Errorf("swap still active: %v, %v", active, err)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "linux_filesystem" "swap" {
  device = %q
  type   = "swap"
}

resource "linux_mount" "swap" {
  device = "UUID=${linux_filesystem.swap.uuid}"
  fstype = "swap"
  fstab  = %q
}
`, dev, fstab),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("linux_mount.swap", "mounted", "true"),
					func(*terraform.State) error {
						active, err := testClient(t).SwapActive(context.Background(), dev)
						if err != nil || !active {
							return fmt.Errorf("swap not active: %v", err)
						}
						return nil
					},
				),
			},
		},
	})
}

func TestAccBlockDevices(t *testing.T) {
	dir := path.Dir(testPath("x"))
	testAccPreCheckDisk(t)
	cleanupRemoteDir(t, dir+"/x")
	dev := testLoopDevice(t, dir, 16)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheckDisk(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
data "linux_block_devices" "all" {
  wait_for_disks = 1
}

output "loop" {
  value = one([for d in data.linux_block_devices.all.devices : "${d.type}:${d.size}" if d.path == %q])
}
`, dev),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("loop", "loop:"+strconv.Itoa(16<<20)),
					resource.TestCheckResourceAttrSet("data.linux_block_devices.all", "root_disk"),
				),
			},
			{
				Config: `
data "linux_block_devices" "all" {
  wait_for_disks = 1000
  wait_timeout   = "1s"
}
`,
				ExpectError: errorPattern(`expected at least 1000`),
			},
		},
	})
}
