package resources

import (
	"fmt"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/labbs/terraform-provider-linux/pkg/client"
	"github.com/labbs/terraform-provider-linux/pkg/common"
)

func FileResource() *schema.Resource {
	return &schema.Resource{
		Create: createFileResource(),
		Read:   readFileResource(),
		Update: updateFileResource(),
		Delete: deleteFileResource(),

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
		c := m.(*client.Client)
		path := d.Get("path").(string)
		content := d.Get("content").(string)
		owner := d.Get("owner").(string)
		mode := d.Get("mode").(int)

		var command string
		command = fmt.Sprintf("cat <<EOF > %s\n%s\nEOF", path, content)
		_, _, err := client.Command(c, false, command, "")
		if err != nil {
			return fmt.Errorf("error creating file: %s", err)
		}

		if owner != "" {
			command = fmt.Sprint("chown ", owner, " ", path)
			_, _, err = client.Command(c, false, command, "")
			if err != nil {
				return fmt.Errorf("error setting owner: %s", err)
			}
		}

		if mode != 0 {
			command = fmt.Sprintf("chmod %o %s", mode, path)
			_, _, err = client.Command(c, false, command, "")
			if err != nil {
				return fmt.Errorf("error setting mode: %s", err)
			}
		}

		d.SetId(path)
		return readFileResource()(d, m)
	}
}

func readFileResource() func(*schema.ResourceData, interface{}) error {
	return func(d *schema.ResourceData, m interface{}) error {
		c := m.(*client.Client)
		id := d.Id()

		userGroup, mode, err := common.GetFileFolderDetails(c, id)
		if err != nil {
			if err.Error() == "file or folder not found" {
				d.SetId("")
				return nil
			}
			return fmt.Errorf("error reading file: %s", err)
		}

		command := fmt.Sprintf("cat %s", id)
		stdout, _, err := client.Command(c, false, command, "")
		if err != nil {
			return fmt.Errorf("error reading file: %s", err)
		}
		d.Set("content", stdout)
		d.Set("owner", userGroup)
		d.Set("mode", mode)
		return nil
	}
}

func updateFileResource() func(*schema.ResourceData, interface{}) error {
	return func(d *schema.ResourceData, m interface{}) error {
		c := m.(*client.Client)
		currentId := d.Id()

		currentUserGroup, currentMode, err := common.GetFileFolderDetails(c, currentId)
		if err != nil {
			if err.Error() == "file or folder not found" {
				d.SetId("")
				return nil
			}
			return fmt.Errorf("error updating file: %s", err)
		}

		command := fmt.Sprintf("cat %s", currentId)
		stdout, _, err := client.Command(c, false, command, "")
		if err != nil {
			return fmt.Errorf("error get content file: %s", err)
		}
		if stdout != d.Get("content").(string) {
			command = fmt.Sprintf("cat <<EOF > %s\n%s\nEOF", currentId, d.Get("content").(string))
			_, _, err = client.Command(c, false, command, "")
			if err != nil {
				return fmt.Errorf("error updating content file: %s", err)
			}
		}

		if currentId != d.Get("path").(string) {
			command := fmt.Sprintf("mv %s %s", currentId, d.Get("path").(string))
			_, _, err = client.Command(c, false, command, "")
			if err != nil {
				return fmt.Errorf("error moving file: %s", err)
			}
			d.SetId(d.Get("path").(string))
		}

		if currentUserGroup != d.Get("owner").(string) {
			command := fmt.Sprintf("chown %s %s", d.Get("owner").(string), d.Get("path").(string))
			_, _, err = client.Command(c, false, command, "")
			if err != nil {
				return fmt.Errorf("error updating owner file: %s", err)
			}
		}

		if currentMode != d.Get("mode").(int) {
			command := fmt.Sprintf("chmod %o %s", d.Get("mode").(int), d.Get("path").(string))
			_, _, err = client.Command(c, false, command, "")
			if err != nil {
				return fmt.Errorf("error updating mode file: %s", err)
			}
		}

		return readFileResource()(d, m)
	}
}

func deleteFileResource() func(*schema.ResourceData, interface{}) error {
	return func(d *schema.ResourceData, m interface{}) error {
		c := m.(*client.Client)
		id := d.Id()

		command := fmt.Sprintf("rm -f %s", id)
		_, _, err := client.Command(c, false, command, "")
		if err != nil {
			return fmt.Errorf("error deleting file: %s", err)
		}
		return nil
	}
}
