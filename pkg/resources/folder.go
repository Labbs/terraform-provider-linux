package resources

import (
	"fmt"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/labbs/terraform-provider-linux/pkg/client"
	"github.com/labbs/terraform-provider-linux/pkg/common"
)

func FolderResource() *schema.Resource {
	return &schema.Resource{
		Create: createFolderResource(),
		Read:   readFolderResource(),
		Update: updateFolderResource(),
		Delete: deleteFolderResource(),

		Schema: map[string]*schema.Schema{
			"path": {
				Type:        schema.TypeString,
				Description: "The path of the folder.",
				Required:    true,
			},
			"owner": {
				Type:        schema.TypeString,
				Description: "The owner of the folder.",
				Optional:    true,
				Default:     "",
			},
			"mode": {
				Type:        schema.TypeInt,
				Description: "The permissions of the folder.",
				Optional:    true,
				Computed:    true,
			},
			"force": {
				Type:        schema.TypeBool,
				Description: "Force delete the folder.",
				Optional:    true,
				Default:     false,
			},
		},
	}
}

func createFolderResource() func(*schema.ResourceData, interface{}) error {
	return func(d *schema.ResourceData, m interface{}) error {
		c := m.(*client.Client)
		path := d.Get("path").(string)
		owner := d.Get("owner").(string)
		mode := d.Get("mode").(int)

		var command string
		command = fmt.Sprintf("mkdir -p %s", path)
		_, _, err := client.Command(c, false, command, "")
		if err != nil {
			return fmt.Errorf("error creating folder: %s", err)
		}

		if owner != "" {
			command = fmt.Sprintf("chown %s %s", owner, path)
			_, _, err = client.Command(c, false, command, "")
			if err != nil {
				return fmt.Errorf("error setting owner of folder: %s", err)
			}
		}

		if mode != 0 {
			command = fmt.Sprintf("chmod %d %s", mode, path)
			_, _, err = client.Command(c, false, command, "")
			if err != nil {
				return fmt.Errorf("error setting mode of folder: %s", err)
			}
		}

		d.SetId(path)
		return readFolderResource()(d, m)
	}
}

func readFolderResource() func(*schema.ResourceData, interface{}) error {
	return func(d *schema.ResourceData, m interface{}) error {
		c := m.(*client.Client)
		id := d.Id()

		userGroup, mode, err := common.GetFileFolderDetails(c, id)
		if err != nil {
			if err.Error() == "file or folder not found" {
				d.SetId("")
				return nil
			}
			return fmt.Errorf("error reading folder: %s", err)
		}

		d.Set("owner", userGroup)
		d.Set("mode", mode)
		return nil
	}
}

func updateFolderResource() func(*schema.ResourceData, interface{}) error {
	return func(d *schema.ResourceData, m interface{}) error {
		c := m.(*client.Client)
		currentId := d.Id()

		currentUserGroup, currentMode, err := common.GetFileFolderDetails(c, currentId)
		if err != nil {
			if err.Error() == "file or folder not found" {
				d.SetId("")
				return nil
			}
			return fmt.Errorf("error reading folder: %s", err)
		}

		if currentId != d.Get("path").(string) {
			command := fmt.Sprintf("mv %s %s", currentId, d.Get("path").(string))
			_, _, err = client.Command(c, false, command, "")
			if err != nil {
				return fmt.Errorf("error moving folder: %s", err)
			}
			d.SetId(d.Get("path").(string))
		}

		if currentUserGroup != d.Get("owner").(string) {
			command := fmt.Sprintf("chown %s %s", d.Get("owner").(string), d.Get("path").(string))
			_, _, err = client.Command(c, false, command, "")
			if err != nil {
				return fmt.Errorf("error setting owner of folder: %s", err)
			}
		}

		if currentMode != d.Get("mode").(int) {
			command := fmt.Sprintf("chmod %d %s", d.Get("mode").(int), d.Get("path").(string))
			_, _, err = client.Command(c, false, command, "")
			if err != nil {
				return fmt.Errorf("error setting mode of folder: %s", err)
			}
		}

		return readFolderResource()(d, m)
	}
}

func deleteFolderResource() func(*schema.ResourceData, interface{}) error {
	return func(d *schema.ResourceData, m interface{}) error {
		c := m.(*client.Client)
		id := d.Id()
		force := d.Get("force").(bool)

		var command string
		if force {
			command = fmt.Sprintf("rm -rf %s", id)
		} else {
			command = fmt.Sprintf("rmdir %s", id)
		}
		_, _, err := client.Command(c, false, command, "")
		if err != nil {
			return fmt.Errorf("error deleting folder: %s", err)
		}

		return nil
	}
}
