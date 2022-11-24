package pkg

import (
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/labbs/terraform-provider-linux/pkg/client"
	"github.com/spf13/cast"
)

func paramsFromSchema(d *schema.ResourceData) *client.Conn {
	return &client.Conn{
		Host:       cast.ToString(d.Get(attrProviderHost)),
		Port:       cast.ToInt(d.Get(attrProviderPort)),
		User:       cast.ToString(d.Get(attrProviderUser)),
		Password:   cast.ToString(d.Get(attrProviderPassword)),
		PrivateKey: cast.ToString(d.Get(attrProviderPrivateKey)),
		UseSudo:    cast.ToBool(d.Get(attrProviderUseSudo)),
	}
}
