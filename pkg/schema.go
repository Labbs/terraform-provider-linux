package pkg

import "github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

const (
	attrProviderID = "id"

	attrProviderHost    = "host"
	attrProviderPort    = "port"
	attrProviderHostKey = "host_key"

	attrProviderUser       = "user"
	attrProviderPassword   = "password"
	attrProviderPrivateKey = "private_key"

	attrProviderUseSudo = "use_sudo"
)

var schemaProvider = map[string]*schema.Schema{
	attrProviderHost: {
		Type:        schema.TypeString,
		Description: "The host of the resource to manage.",
		Optional:    true,
		Default:     "127.0.0.1",
	},

	attrProviderPort: {
		Type:        schema.TypeInt,
		Description: "The port of the resource to manage.",
		Optional:    true,
		Default:     22,
	},

	attrProviderHostKey: {
		Type:        schema.TypeString,
		Description: "The host key of the resource to manage.",
		Optional:    true,
		Default:     "",
	},

	attrProviderUser: {
		Type:        schema.TypeString,
		Description: "The user of the resource to manage.",
		Optional:    true,
		Default:     "root",
	},

	attrProviderPassword: {
		Type:        schema.TypeString,
		Description: "The password of the resource to manage.",
		Optional:    true,
		Default:     "",
	},

	attrProviderPrivateKey: {
		Type:        schema.TypeString,
		Description: "The private key of the resource to manage.",
		Optional:    true,
		Default:     "",
	},

	attrProviderUseSudo: {
		Type:        schema.TypeBool,
		Description: "The use sudo of the resource to manage.",
		Optional:    true,
		Default:     false,
	},
}
