package provider

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/labbs/terraform-provider-linux/internal/textedit"
)

func TestAccCronJob(t *testing.T) {
	name := "tf-acc-" + acctest.RandString(6)
	config := func(schedule string) string {
		return fmt.Sprintf(`
resource "linux_cron_job" "test" {
  name     = %q
  user     = "root"
  schedule = %q
  command  = "/usr/bin/true > /dev/null 2>&1"
}
`, name, schedule)
	}
	checkJob := func(schedule string) resource.TestCheckFunc {
		return func(*terraform.State) error {
			crontab, err := testClient(t).Crontab(context.Background(), "root")
			if err != nil {
				return err
			}
			job, ok := textedit.FindCronJob(crontab, name)
			if !ok || job.Schedule != schedule {
				return fmt.Errorf("unexpected crontab:\n%s", crontab)
			}
			return nil
		}
	}
	removeJob := func() {
		_, err := testClient(t).EditCrontab(context.Background(), "root", func(s string) (string, error) {
			return textedit.RemoveCronJob(s, name), nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheckRoot(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy: func(*terraform.State) error {
			crontab, err := testClient(t).Crontab(context.Background(), "root")
			if _, ok := textedit.FindCronJob(crontab, name); ok || err != nil {
				return fmt.Errorf("job still there (%v):\n%s", err, crontab)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: config("*/5 * * * *"), Check: checkJob("*/5 * * * *")},
			{Config: config("@daily"), Check: checkJob("@daily")},
			{
				PreConfig:          removeJob,
				Config:             config("@daily"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{Config: config("@daily"), Check: checkJob("@daily")},
		},
	})
}
