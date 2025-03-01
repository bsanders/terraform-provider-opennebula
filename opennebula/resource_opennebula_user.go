package opennebula

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

	"github.com/OpenNebula/one/src/oca/go/src/goca"
	dyn "github.com/OpenNebula/one/src/oca/go/src/goca/dynamic"
	"github.com/OpenNebula/one/src/oca/go/src/goca/parameters"
	"github.com/OpenNebula/one/src/oca/go/src/goca/schemas/user"
)

var authTypes = []string{"core", "public", "ssh", "x509", "ldap", "server_cipher", "server_x509", "custom"}

func resourceOpennebulaUser() *schema.Resource {
	return &schema.Resource{
		CreateContext: resourceOpennebulaUserCreate,
		ReadContext:   resourceOpennebulaUserRead,
		UpdateContext: resourceOpennebulaUserUpdate,
		DeleteContext: resourceOpennebulaUserDelete,
		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},

		Schema: map[string]*schema.Schema{
			"name": {
				Type:        schema.TypeString,
				Required:    true,
				Description: "Name of the User",
			},
			"password": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "Password of the User. Required for all `auth_driver` options excepted 'ldap'",
			},
			"auth_driver": {
				Type:        schema.TypeString,
				Optional:    true,
				Default:     "core",
				Description: "Authentication driver. Select between: core, public, ssh, x509, ldap, server_cipher, server_x509 and custom. Defaults to 'core'.",
				ValidateFunc: func(v interface{}, k string) (ws []string, errors []error) {
					value := v.(string)

					if inArray(value, authTypes) < 0 {
						errors = append(errors, fmt.Errorf("Auth driver %q must be one of: %s", k, strings.Join(locktypes, ",")))
					}

					return
				},
			},
			"primary_group": {
				Type:        schema.TypeInt,
				Optional:    true,
				Default:     0,
				Description: "Primary (Default) Group ID of the user. Defaults to 0",
			},
			"groups": {
				Type:        schema.TypeList,
				Optional:    true,
				Description: "List of group IDs to add to the user",
				Elem: &schema.Schema{
					Type: schema.TypeInt,
				},
			},
			"quotas": quotasSchema(),
			"tags":   tagsSchema(),
		},
	}
}

func getUserController(d *schema.ResourceData, meta interface{}) (*goca.UserController, error) {
	config := meta.(*Configuration)
	controller := config.Controller
	var uc *goca.UserController

	// Try to find the User by ID, if specified
	if d.Id() != "" {
		uid, err := strconv.ParseUint(d.Id(), 10, 0)
		if err != nil {
			return nil, err
		}
		uc = controller.User(int(uid))
	}

	// Otherwise, try to find the User by name as the de facto compound primary key
	if d.Id() == "" {
		uid, err := controller.Users().ByName(d.Get("name").(string))
		if err != nil {
			return nil, err
		}
		uc = controller.User(uid)
	}

	return uc, nil
}

func resourceOpennebulaUserCreate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	config := meta.(*Configuration)
	controller := config.Controller

	var diags diag.Diagnostics

	userName := d.Get("name").(string)
	userAuthDriver := d.Get("auth_driver").(string)
	var userPassword string
	if userAuthDriver != "ldap" {
		userPassword_interface, ok := d.GetOk("password")
		if !ok {
			diags = append(diags, diag.Diagnostic{
				Severity: diag.Error,
				Summary:  "Empty password",
				Detail:   fmt.Sprintf("Password cannot be empty if auth_driver is: %s", userAuthDriver),
			})
			return diags
		}
		userPassword = userPassword_interface.(string)
	}
	userGroupLists := d.Get("groups").([]interface{})
	userGroups := make([]int, 0, 1+len(userGroupLists))

	// Start Group array with Primary group
	userGroups = append(userGroups, d.Get("primary_group").(int))

	// add groups to user if list provided
	for _, gid := range userGroupLists {
		userGroups = append(userGroups, gid.(int))
	}

	userID, err := controller.Users().Create(userName, userPassword, userAuthDriver, userGroups)
	if err != nil {
		diags = append(diags, diag.Diagnostic{
			Severity: diag.Error,
			Summary:  "Failed to create the user",
			Detail:   err.Error(),
		})
		return diags
	}
	d.SetId(fmt.Sprintf("%v", userID))

	uc := controller.User(userID)

	if _, ok := d.GetOk("quotas"); ok {
		quotasStr, err := generateQuotas(d)
		if err != nil {
			diags = append(diags, diag.Diagnostic{
				Severity: diag.Error,
				Summary:  "Failed to generate quotas description",
				Detail:   fmt.Sprintf("user (ID: %s): %s", d.Id(), err),
			})
			return diags
		}
		err = uc.Quota(quotasStr)
		if err != nil {
			diags = append(diags, diag.Diagnostic{
				Severity: diag.Error,
				Summary:  "Failed to apply quotas",
				Detail:   fmt.Sprintf("user (ID: %s): %s", d.Id(), err),
			})
			return diags
		}
	}

	tpl := dyn.NewTemplate()

	tagsInterface := d.Get("tags").(map[string]interface{})
	for k, v := range tagsInterface {
		tpl.AddPair(strings.ToUpper(k), v)
	}

	if len(tpl.Elements) > 0 {
		err = uc.Update(tpl.String(), parameters.Merge)
		if err != nil {
			diags = append(diags, diag.Diagnostic{
				Severity: diag.Error,
				Summary:  "Failed to update content",
				Detail:   fmt.Sprintf("user (ID: %s): %s", d.Id(), err),
			})
			return diags
		}
	}

	return resourceOpennebulaUserRead(ctx, d, meta)
}

func resourceOpennebulaUserRead(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {

	var diags diag.Diagnostics

	uc, err := getUserController(d, meta)
	if err != nil {
		if NoExists(err) {
			log.Printf("[WARN] Removing user %s from state because it no longer exists in", d.Get("name"))
			d.SetId("")
			return nil
		}

		diags = append(diags, diag.Diagnostic{
			Severity: diag.Error,
			Summary:  "Failed to get the user controller",
			Detail:   err.Error(),
		})
		return diags
	}

	// TODO: fix it after 5.10 release
	// Force the "decrypt" bool to false to keep ONE 5.8 behavior
	user, err := uc.Info(false)
	if err != nil {
		if err != nil {
			diags = append(diags, diag.Diagnostic{
				Severity: diag.Error,
				Summary:  "Failed to retrieve informations",
				Detail:   fmt.Sprintf("user (ID: %s): %s", d.Id(), err),
			})
			return diags
		}
	}

	d.SetId(strconv.FormatUint(uint64(user.ID), 10))
	d.Set("name", user.Name)

	passwordIf := d.Get("password")
	password := passwordIf.(string)
	sum := sha256.Sum256([]byte(password))
	if fmt.Sprintf("%x", sum) == user.Password {
		d.Set("password", password)
	} else {
		if err != nil {
			diags = append(diags, diag.Diagnostic{
				Severity: diag.Error,
				Summary:  "Password doesn't match",
				Detail:   fmt.Sprintf("user (ID: %s): %s", d.Id(), err),
			})
			return diags
		}
	}

	d.Set("auth_driver", user.AuthDriver)
	d.Set("primary_group", user.GID)

	err = flattenUserGroups(d, user)
	if err != nil {
		diags = append(diags, diag.Diagnostic{
			Severity: diag.Error,
			Summary:  "Failed to flatten groups",
			Detail:   fmt.Sprintf("user (ID: %s): %s", d.Id(), err),
		})
		return diags
	}

	err = flattenQuotasMapFromStructs(d, &user.QuotasList)
	if err != nil {
		diags = append(diags, diag.Diagnostic{
			Severity: diag.Error,
			Summary:  "Failed to flatten quotas",
			Detail:   fmt.Sprintf("user (ID: %s): %s", d.Id(), err),
		})
		return diags
	}

	err = flattenUserTemplate(d, &user.Template)
	if err != nil {
		diags = append(diags, diag.Diagnostic{
			Severity: diag.Error,
			Summary:  "Failed to flatten template",
			Detail:   fmt.Sprintf("user (ID: %s): %s", d.Id(), err),
		})
		return diags
	}

	return nil
}

func flattenUserGroups(d *schema.ResourceData, user *user.User) error {

	userGroups := make([]int, 0)
	for _, u := range user.Groups.ID {
		if u == user.GID {
			continue
		}
		userGroups = append(userGroups, u)
	}
	if len(userGroups) > 0 {
		err := d.Set("groups", userGroups)
		if err != nil {
			return err
		}
	}

	return nil
}

func flattenUserTemplate(d *schema.ResourceData, userTpl *dyn.Template) error {

	tags := make(map[string]interface{})
	tagsInterface, tagsOk := d.GetOk("tags")
	for i, _ := range userTpl.Elements {
		pair, ok := userTpl.Elements[i].(*dyn.Pair)
		if !ok || !tagsOk {
			continue
		}

		for k, _ := range tagsInterface.(map[string]interface{}) {
			if strings.ToUpper(k) == pair.Key() {
				tags[k] = pair.Value
			}
		}

	}

	if tagsOk {
		err := d.Set("tags", tags)
		if err != nil {
			return err
		}
	}

	return nil
}

func resourceOpennebulaUserUpdate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {

	var diags diag.Diagnostics

	uc, err := getUserController(d, meta)
	if err != nil {
		diags = append(diags, diag.Diagnostic{
			Severity: diag.Error,
			Summary:  "Failed to get the user controller",
			Detail:   err.Error(),
		})
		return diags
	}

	if d.HasChange("password") {
		// update password
		err = uc.Passwd(d.Get("password").(string))
		if err != nil {
			diags = append(diags, diag.Diagnostic{
				Severity: diag.Error,
				Summary:  "Failed to update password",
				Detail:   fmt.Sprintf("user (ID: %s): %s", d.Id(), err),
			})
			return diags
		}
	}

	if d.HasChange("auth_driver") {
		// Erase previous authentication driver, let password unchanged
		err = uc.Chauth(d.Get("auth_driver").(string), "")
		if err != nil {
			diags = append(diags, diag.Diagnostic{
				Severity: diag.Error,
				Summary:  "Failed to change authentication driver",
				Detail:   fmt.Sprintf("user (ID: %s): %s", d.Id(), err),
			})
			return diags
		}
	}

	if d.HasChange("primary_group") {
		// change the primary group of the User
		err = uc.Chgrp(d.Get("primary_group").(int))
		if err != nil {
			diags = append(diags, diag.Diagnostic{
				Severity: diag.Error,
				Summary:  "Failed to change group",
				Detail:   fmt.Sprintf("user (ID: %s): %s", d.Id(), err),
			})
			return diags
		}
	}

	if d.HasChange("groups") {
		// Update secondary group list
		oGroupsInterface, nGroupsInterface := d.GetChange("groups")
		oGroups := oGroupsInterface.([]interface{})
		nGroups := nGroupsInterface.([]interface{})
		for _, g := range oGroups {
			if g.(int) == d.Get("primary_group").(int) {
				continue
			}
			err = uc.DelGroup(g.(int))
			if err != nil {
				diags = append(diags, diag.Diagnostic{
					Severity: diag.Error,
					Summary:  "Failed to delete group",
					Detail:   fmt.Sprintf("user (ID: %s): %s", d.Id(), err),
				})
				return diags
			}
		}
		for _, g := range nGroups {
			if g.(int) == d.Get("primary_group").(int) {
				continue
			}
			err = uc.AddGroup(g.(int))
			if err != nil {
				diags = append(diags, diag.Diagnostic{
					Severity: diag.Error,
					Summary:  "Failed to add group",
					Detail:   fmt.Sprintf("user (ID: %s): %s", d.Id(), err),
				})
				return diags
			}
		}
	}

	if d.HasChange("quotas") {
		if _, ok := d.GetOk("quotas"); ok {
			quotasStr, err := generateQuotas(d)
			if err != nil {
				if err != nil {
					diags = append(diags, diag.Diagnostic{
						Severity: diag.Error,
						Summary:  "Failed to generate quotas description",
						Detail:   fmt.Sprintf("user (ID: %s): %s", d.Id(), err),
					})
					return diags
				}
			}
			err = uc.Quota(quotasStr)
			if err != nil {
				diags = append(diags, diag.Diagnostic{
					Severity: diag.Error,
					Summary:  "Failed apply quotas",
					Detail:   fmt.Sprintf("user (ID: %s): %s", d.Id(), err),
				})
				return diags
			}
		}
	}

	userInfos, err := uc.Info(false)
	if err != nil {
		diags = append(diags, diag.Diagnostic{
			Severity: diag.Error,
			Summary:  "Failed to retrieve informations",
			Detail:   fmt.Sprintf("user (ID: %s): %s", d.Id(), err),
		})
		return diags
	}

	update := false
	newTpl := userInfos.Template

	if d.HasChange("tags") {

		oldTagsIf, newTagsIf := d.GetChange("tags")
		oldTags := oldTagsIf.(map[string]interface{})
		newTags := newTagsIf.(map[string]interface{})

		// delete tags
		for k, _ := range oldTags {
			_, ok := newTags[k]
			if ok {
				continue
			}
			newTpl.Del(strings.ToUpper(k))
		}

		// add/update tags
		for k, v := range newTags {
			newTpl.Del(strings.ToUpper(k))
			newTpl.AddPair(strings.ToUpper(k), v)
		}

		update = true
	}

	if update {
		err = uc.Update(newTpl.String(), parameters.Replace)
		if err != nil {
			diags = append(diags, diag.Diagnostic{
				Severity: diag.Error,
				Summary:  "Failed to update content",
				Detail:   fmt.Sprintf("user (ID: %s): %s", d.Id(), err),
			})
			return diags
		}
	}

	return resourceOpennebulaUserRead(ctx, d, meta)
}

func resourceOpennebulaUserDelete(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {

	var diags diag.Diagnostics

	gc, err := getUserController(d, meta)
	if err != nil {
		diags = append(diags, diag.Diagnostic{
			Severity: diag.Error,
			Summary:  "Failed to get the user controller",
			Detail:   err.Error(),
		})
		return diags
	}

	err = gc.Delete()
	if err != nil {
		diags = append(diags, diag.Diagnostic{
			Severity: diag.Error,
			Summary:  "Failed to delete",
			Detail:   fmt.Sprintf("user (ID: %s): %s", d.Id(), err),
		})
		return diags
	}

	return nil
}
