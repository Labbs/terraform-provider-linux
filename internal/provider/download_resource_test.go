package provider

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/labbs/terraform-provider-linux/internal/system"
)

// testTarGz builds a gzipped tarball of files, all under the directory
// root, with an owner that does not exist on the host.
func testTarGz(t *testing.T, root string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	_ = tw.WriteHeader(&tar.Header{Name: root + "/", Typeflag: tar.TypeDir, Mode: 0o755, Uid: 4242, Gid: 4242})
	for name, content := range files {
		mode := int64(0o644)
		if strings.HasPrefix(name, "bin/") {
			mode = 0o755
		}
		h := &tar.Header{Name: root + "/" + name, Mode: mode, Size: int64(len(content)), Uid: 4242, Gid: 4242}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(content))
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestAccDownloadAndExtract(t *testing.T) {
	base := path.Dir(testPath("x"))
	cleanupRemoteDir(t, base+"/x")
	v1 := testTarGz(t, "pkg-1.0", map[string]string{"bin/tool": "tool v1\n", "LICENSE": "license\n", "OLD": "only in v1\n"})
	v2 := testTarGz(t, "pkg-2.0", map[string]string{"bin/tool": "tool v2\n", "LICENSE": "license\n"})
	dl := base + "/dl/pkg.tar.gz"
	dest := base + "/opt/pkg"

	config := func(version, sha256, mode string) string {
		checksum := ""
		if sha256 != "" {
			checksum = fmt.Sprintf("sha256 = %q", sha256)
		}
		return fmt.Sprintf(`
resource "linux_download" "pkg" {
  url  = "file://%[1]s/src/pkg-%[2]s.tar.gz"
  path = %[3]q
  mode = %[4]q
  %[5]s
}

resource "linux_archive_extract" "pkg" {
  source           = linux_download.pkg.path
  destination      = %[6]q
  strip_components = 1

  triggers = {
    sha256 = linux_download.pkg.sha256
  }
}
`, base, version, dl, mode, checksum, dest)
	}

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheckLinux(t)
			c := testClient(t)
			for name, data := range map[string][]byte{"pkg-1.0.tar.gz": v1, "pkg-2.0.tar.gz": v2} {
				if err := c.WriteFile(context.Background(), base+"/src/"+name, bytes.NewReader(data), system.Attributes{}, true); err != nil {
					t.Fatal(err)
				}
			}
		},
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy: resource.ComposeTestCheckFunc(
			checkRemoteMissing(t, dl),
			checkRemoteMissing(t, dest+"/bin/tool"),
			checkRemoteMissing(t, dest+"/bin"),
		),
		Steps: []resource.TestStep{
			{
				Config: config("1.0", sha256Hex(v1), "0600"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("linux_download.pkg", "id", dl),
					resource.TestCheckResourceAttr("linux_download.pkg", "sha256", sha256Hex(v1)),
					resource.TestCheckResourceAttr("linux_download.pkg", "mode", "0600"),
					resource.TestCheckResourceAttrSet("linux_download.pkg", "owner"),
					resource.TestCheckResourceAttr("linux_archive_extract.pkg", "files.#", "3"),
					resource.TestCheckResourceAttr("linux_archive_extract.pkg", "files.0", "LICENSE"),
					resource.TestCheckResourceAttr("linux_archive_extract.pkg", "files.1", "OLD"),
					resource.TestCheckResourceAttr("linux_archive_extract.pkg", "files.2", "bin/tool"),
					checkRemoteFile(t, dl, string(v1), "0600"),
					checkRemoteFile(t, dest+"/bin/tool", "tool v1\n", "0755"),
				),
			},
			{
				// A new version, without a known checksum: both are replaced,
				// and the files of the old version are removed.
				Config: config("2.0", "", "0600"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("linux_download.pkg", plancheck.ResourceActionDestroyBeforeCreate),
						plancheck.ExpectResourceAction("linux_archive_extract.pkg", plancheck.ResourceActionDestroyBeforeCreate),
						plancheck.ExpectUnknownValue("linux_download.pkg", tfjsonpath.New("sha256")),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("linux_download.pkg", "sha256", sha256Hex(v2)),
					resource.TestCheckResourceAttr("linux_archive_extract.pkg", "files.#", "2"),
					checkRemoteFile(t, dest+"/bin/tool", "tool v2\n", "0755"),
					checkRemoteMissing(t, dest+"/OLD"),
				),
			},
			{
				// Only the permissions change: the archive is not extracted again.
				Config: config("2.0", "", "0644"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("linux_download.pkg", plancheck.ResourceActionUpdate),
						plancheck.ExpectResourceAction("linux_archive_extract.pkg", plancheck.ResourceActionNoop),
					},
				},
				Check: checkRemoteFile(t, dl, string(v2), "0644"),
			},
			{
				// An extracted file removed outside of Terraform.
				PreConfig: func() {
					if err := testClient(t).Remove(context.Background(), dest+"/bin/tool"); err != nil {
						t.Fatal(err)
					}
				},
				Config:             config("2.0", "", "0644"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: config("2.0", "", "0644"),
				Check:  checkRemoteFile(t, dest+"/bin/tool", "tool v2\n", "0755"),
			},
			{
				// The download changed outside of Terraform.
				PreConfig: func() {
					err := testClient(t).WriteFile(context.Background(), dl, strings.NewReader("tampered"), system.Attributes{}, false)
					if err != nil {
						t.Fatal(err)
					}
				},
				Config:             config("2.0", "", "0644"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: config("2.0", "", "0644"),
				Check:  checkRemoteFile(t, dl, string(v2), "0644"),
			},
		},
	})
}

func TestAccDownloadChecksumMismatch(t *testing.T) {
	base := path.Dir(testPath("x"))
	cleanupRemoteDir(t, base+"/x")
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheckLinux(t)
			if err := testClient(t).WriteFile(context.Background(), base+"/src", strings.NewReader("content"), system.Attributes{}, true); err != nil {
				t.Fatal(err)
			}
		},
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "linux_download" "test" {
  url    = "file://%s/src"
  path   = "%s/dst"
  sha256 = "%s"
}
`, base, base, strings.Repeat("0", 64)),
				ExpectError: regexp.MustCompile(`checksum mismatch`),
			},
		},
	})
}
