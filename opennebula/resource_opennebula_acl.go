package opennebula

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

	"github.com/OpenNebula/one/src/oca/go/src/goca/schemas/acl"
)

var resourceMap = map[string]acl.Resources{
	"VM":             acl.VM,
	"HOST":           acl.Host,
	"NET":            acl.Net,
	"IMAGE":          acl.Image,
	"USER":           acl.User,
	"TEMPLATE":       acl.Template,
	"GROUP":          acl.Group,
	"DATASTORE":      acl.Datastore,
	"CLUSTER":        acl.Cluster,
	"DOCUMENT":       acl.Document,
	"ZONE":           acl.Zone,
	"SECGROUP":       acl.SecGroup,
	"VDC":            acl.Vdc,
	"VROUTER":        acl.VRouter,
	"MARKETPLACE":    acl.MarketPlace,
	"MARKETPLACEAPP": acl.MarketPlaceApp,
	"VMGROUP":        acl.VMGroup,
	"VNTEMPLATE":     acl.VNTemplate,
}

var rightMap = map[string]acl.Rights{
	"USE":    acl.Use,
	"MANAGE": acl.Manage,
	"ADMIN":  acl.Admin,
	"CREATE": acl.Create,
}

func resourceOpennebulaACL() *schema.Resource {
	return &schema.Resource{
		CreateContext: resourceOpennebulaACLCreate,
		ReadContext:   resourceOpennebulaACLRead,
		DeleteContext: resourceOpennebulaACLDelete,
		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},

		Schema: map[string]*schema.Schema{
			"user": {
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "User component of the new rule. ACL String Syntax is expected.",
			},
			"resource": {
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "Resource component of the new rule. ACL String Syntax is expected.",
			},
			"rights": {
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "Rights component of the new rule. ACL String Syntax is expected.",
			},
			"zone": {
				Type:        schema.TypeString,
				Optional:    true,
				ForceNew:    true,
				Description: "Zone component of the new rule. ACL String Syntax is expected.",
			},
		},
	}
}

func resourceOpennebulaACLCreate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	config := meta.(*Configuration)
	controller := config.Controller

	var diags diag.Diagnostics

	userHex, err := acl.ParseUsers(d.Get("user").(string))
	if err != nil {
		diags = append(diags, diag.Diagnostic{
			Severity: diag.Error,
			Summary:  "Failed to parse ACL users",
			Detail:   err.Error(),
		})
		return diags
	}

	resourceHex, err := acl.ParseResources(d.Get("resource").(string))
	if err != nil {
		diags = append(diags, diag.Diagnostic{
			Severity: diag.Error,
			Summary:  "Failed to parse ACL resources",
			Detail:   err.Error(),
		})
		return diags
	}

	rightsHex, err := acl.ParseRights(d.Get("rights").(string))
	if err != nil {
		diags = append(diags, diag.Diagnostic{
			Severity: diag.Error,
			Summary:  "Failed to parse ACL rights",
			Detail:   err.Error(),
		})
		return diags
	}

	var aclID int
	zone := d.Get("zone").(string)
	if len(zone) > 0 {
		zoneHex, err := acl.ParseZone(zone)
		if err != nil {
			diags = append(diags, diag.Diagnostic{
				Severity: diag.Error,
				Summary:  "Failed to parse zone",
				Detail:   err.Error(),
			})
			return diags
		}

		aclID, err = controller.ACLs().CreateRule(userHex, resourceHex, rightsHex, zoneHex)
	} else {
		aclID, err = controller.ACLs().CreateRule(userHex, resourceHex, rightsHex)
	}
	if err != nil {
		diags = append(diags, diag.Diagnostic{
			Severity: diag.Error,
			Summary:  "Failed to create rule",
			Detail:   err.Error(),
		})
		return diags
	}
	d.SetId(fmt.Sprintf("%v", aclID))

	return resourceOpennebulaACLRead(ctx, d, meta)
}

func resourceOpennebulaACLRead(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	config := meta.(*Configuration)
	controller := config.Controller

	var diags diag.Diagnostics

	acls, err := controller.ACLs().Info()
	if err != nil {
		diags = append(diags, diag.Diagnostic{
			Severity: diag.Error,
			Summary:  "Failed to retrieve ACL informations",
			Detail:   err.Error(),
		})
		return diags
	}

	numericID, err := strconv.Atoi(d.Id())
	if err != nil {
		diags = append(diags, diag.Diagnostic{
			Severity: diag.Error,
			Summary:  "Failed to parse the ACL rule ID",
			Detail:   err.Error(),
		})
		return diags
	}

	for _, acl := range acls.ACLs {
		if acl.ID == numericID {
			// We don't call Set because that would overwrite our string values
			// With raw numbers.
			// We only check if an ACL with the given ID exists, and return an error if not.
			return nil
		}
	}

	diags = append(diags, diag.Diagnostic{
		Severity: diag.Error,
		Summary:  fmt.Sprintf("Failed to find ACL rule %s", d.Id()),
		Detail:   err.Error(),
	})

	return diags
}

func resourceOpennebulaACLDelete(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	config := meta.(*Configuration)
	controller := config.Controller

	var diags diag.Diagnostics

	numericID, err := strconv.Atoi(d.Id())
	if err != nil {
		diags = append(diags, diag.Diagnostic{
			Severity: diag.Error,
			Summary:  "Failed to parse ACL rule ID",
			Detail:   err.Error(),
		})
		return diags
	}

	err = controller.ACLs().DeleteRule(numericID)
	if err != nil {
		diags = append(diags, diag.Diagnostic{
			Severity: diag.Error,
			Summary:  "Failed to delete ACL rule",
			Detail:   err.Error(),
		})
	}

	return diags

}
