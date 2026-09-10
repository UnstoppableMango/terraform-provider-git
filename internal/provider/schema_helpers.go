package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
)

// authSchemaAttribute builds the "auth" nested attribute shared by the
// data sources that authenticate against a repository or hosting API,
// keeping its shape and Sensitive marking from drifting between copies.
func authSchemaAttribute(desc string) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Optional:            true,
		MarkdownDescription: desc,
		Attributes: map[string]schema.Attribute{
			"token": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "Token used to authenticate with the repository host.",
			},
		},
	}
}
