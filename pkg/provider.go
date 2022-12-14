package pkg

import (
	"context"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/labbs/terraform-provider-linux/pkg/resources"
)

func Provider() *schema.Provider {
	return &schema.Provider{
		Schema: schemaProvider,
		ConfigureContextFunc: func(ctx context.Context, d *schema.ResourceData) (interface{}, diag.Diagnostics) {
			return paramsFromSchema(d), nil
		},
		DataSourcesMap: map[string]*schema.Resource{},
		ResourcesMap: map[string]*schema.Resource{
			"linux_file":   resources.FileResource(),
			"linux_folder": resources.FolderResource(),
		},
	}
}
