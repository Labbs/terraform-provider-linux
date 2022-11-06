package resource

import (
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/labbs/terraform-provider-linux/pkg"
)

func fileResource() *schema.Resource {
	return &schema.Resource{

		Schema: map[string]*schema.Schema{
			"content": {
				Type:        schema.TypeString,
				Description: "The content of the file.",
				Optional:    true,
				Default:     "",
			},
			"owner": {
				Type:        schema.TypeString,
				Description: "The owner of the file.",
				Optional:    true,
				Default:     "",
			},
			"mode": {
				Type:        schema.TypeInt,
				Description: "The permissions of the file.",
				Optional:    true,
				Computed:    true,
			},
			"path": {
				Type:        schema.TypeString,
				Description: "The path of the file.",
				Required:    true,
			},
		},
	}
}

func createFileResource() func(*schema.ResourceData, interface{}) error {
	return func(d *schema.ResourceData, m interface{}) error {
		client := m.(*pkg.Client)

	}
}
