// package version_1_10_0 Handle the migration to v1.10.0
package version_1_10_0

import (
	"context"
	"fmt"
	"strings"

	"github.com/enrichman/kubectl-rancher-migration/pkg/client"
	"github.com/fatih/color"
	ldapv3 "github.com/go-ldap/ldap/v3"
	apiv3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	ad "github.com/rancher/rancher/pkg/auth/providers/activedirectory"
	"github.com/rancher/rancher/pkg/auth/providers/activedirectory/guid"
	"github.com/rancher/rancher/pkg/auth/providers/common/ldap"
)

var (
	blue   = color.New(color.FgBlue).SprintFunc()
	green  = color.New(color.FgGreen).SprintFunc()
	red    = color.New(color.FgRed).SprintFunc()
	yellow = color.New(color.FgYellow).SprintFunc()
)

func Check(c *client.RancherClient, lConn *client.LdapClient, config *apiv3.ActiveDirectoryConfig) error {
	fmt.Println("Check")

	migratable, err := GetMigratableResources(c, lConn.Conn, config)
	if err != nil {
		return err
	}

	dnResources := migratable.WithDNs()
	guidResources := migratable.WithGUIDs()

	fmt.Printf(
		"Found %d resource groups that can be moved (%d containing DNs and %d containing objectGUIDs).\n\n",
		len(migratable), len(dnResources), len(guidResources),
	)

	fmt.Println("DN resources:")
	for i, res := range dnResources {
		fmt.Printf("%00d) %s\n", i+1, blue(res.PrincipalID))
		fmt.Printf("\tGUID:\t%s\n", green(res.GUID.UUID()))

		prtbs := GetResourceByType[*PRTBResource](res.Bindings)
		fmt.Printf("\tProjectRoleTemplateBindings (%d)\n", len(prtbs))
		for _, prtb := range prtbs {
			fmt.Printf("\t- Namespace: %s, Name: %s\n", yellow(prtb.PRTB.Namespace), yellow(prtb.PRTB.Name))
		}

		crtbs := GetResourceByType[*CRTBResource](res.Bindings)
		fmt.Printf("\tClusterRoleTemplateBindings (%d)\n", len(crtbs))
		for _, crtb := range crtbs {
			fmt.Printf("\t- Namespace: %s, Name: %s\n", yellow(crtb.CRTB.Namespace), yellow(crtb.CRTB.Name))
		}

		tokens := GetResourceByType[*TokenResource](res.Bindings)
		fmt.Printf("\tTokens (%d)\n", len(tokens))
		for _, token := range tokens {
			fmt.Printf("\t- Namespace: %s, Name: %s\n", yellow(token.Token.Namespace), yellow(token.Token.Name))
		}
	}

	fmt.Println("GUID resources:")
	for i, res := range guidResources {
		fmt.Printf("%00d) %s\n", i+1, blue(res.PrincipalID))
		fmt.Printf("\tDN:\t%s\n", green(res.DN))

		prtbs := GetResourceByType[*PRTBResource](res.Bindings)
		fmt.Printf("\tProjectRoleTemplateBindings (%d)\n", len(prtbs))
		for _, prtb := range prtbs {
			fmt.Printf("\t- Namespace: %s, Name: %s\n", yellow(prtb.PRTB.Namespace), yellow(prtb.PRTB.Name))
		}

		crtbs := GetResourceByType[*CRTBResource](res.Bindings)
		fmt.Printf("\tClusterRoleTemplateBindings (%d)\n", len(crtbs))
		for _, crtb := range crtbs {
			fmt.Printf("\t- Namespace: %s, Name: %s\n", yellow(crtb.CRTB.Namespace), yellow(crtb.CRTB.Name))
		}

		tokens := GetResourceByType[*TokenResource](res.Bindings)
		fmt.Printf("\tTokens (%d)\n", len(tokens))
		for _, token := range tokens {
			fmt.Printf("\t- Name: %s\n", yellow(token.Token.Name))
		}
	}

	return nil
}

func Migrate(c *client.RancherClient, lConn *client.LdapClient, config *apiv3.ActiveDirectoryConfig, principalIDs []string) error {
	fmt.Println("Start migration...")

	migratable, err := GetMigratableResources(c, lConn.Conn, config)
	if err != nil {
		return err
	}

	if len(principalIDs) > 0 {
		filtered := MigratableResources{}
		for _, pID := range principalIDs {
			filtered[pID] = migratable[pID]
		}
		migratable = filtered
	}

	dnResources := migratable.WithDNs()

	return UpdateResources(c, dnResources)
}

func Rollback(c *client.RancherClient, lConn *client.LdapClient, config *apiv3.ActiveDirectoryConfig, principalIDs []string) error {
	fmt.Println("Start rollback")

	migratable, err := GetMigratableResources(c, lConn.Conn, config)
	if err != nil {
		return err
	}

	if len(principalIDs) > 0 {
		filtered := MigratableResources{}
		for _, pID := range principalIDs {
			filtered[pID] = migratable[pID]
		}
		migratable = filtered
	}

	guidResources := migratable.WithGUIDs()

	return UpdateResources(c, guidResources)
}

// GetMigratableResources will return a map of resources that can be migrated or rolled back
func GetMigratableResources(c *client.RancherClient, lConn *ldapv3.Conn, config *apiv3.ActiveDirectoryConfig) (MigratableResources, error) {
	resourcesToMigrate := map[string]*MigratableResource{}

	userMap, err := GetUsersToMigrate(c)
	if err != nil {
		return nil, err
	}

	for principalID, user := range userMap {
		resourcesToMigrate[principalID] = &MigratableResource{
			PrincipalID: principalID,
			User:        user,
		}
	}

	bindingsMap, err := GetUserBindings(c)
	if err != nil {
		return nil, err
	}

	for principalID, binds := range bindingsMap {
		// check if principalID alrteady exists in map, otherwise this resource is "orphaned"
		res, found := resourcesToMigrate[principalID]
		if !found {
			res = &MigratableResource{
				PrincipalID: principalID,
			}
		}
		res.Bindings = binds

		resourcesToMigrate[principalID] = res
	}

	// fill DN and/or GUID
	for principalID, res := range resourcesToMigrate {
		objectGUIDPrincipalPrefix := fmt.Sprintf("%s://%s=", ad.UserScope, ad.ObjectGUIDAttribute)

		if strings.HasPrefix(principalID, objectGUIDPrincipalPrefix) {
			objectGUID := strings.TrimPrefix(principalID, objectGUIDPrincipalPrefix)

			parsedGUID, err := guid.Parse(objectGUID)
			if err != nil {
				return nil, err
			}

			res.GUID = parsedGUID
			res.DN, err = getDN(lConn, config, parsedGUID)
			if err != nil {
				return nil, err
			}

		} else {
			res.DN = strings.TrimPrefix(principalID, ad.UserScope+"://")
			res.GUID, err = getGUID(lConn, config, res.DN)
			if err != nil {
				return nil, err
			}
		}
	}

	return resourcesToMigrate, nil
}

// GetUsersToMigrate will fetch all the users with an old activedirectory PrincipalID
func GetUsersToMigrate(c *client.RancherClient) (map[string]*apiv3.User, error) {
	users := &apiv3.UserList{}
	err := c.Rancher.Get().Resource("users").Do(context.Background()).Into(users)
	if err != nil {
		return nil, err
	}

	migratableUsers := map[string]*apiv3.User{}

	for _, user := range users.Items {
		for _, principalID := range user.PrincipalIDs {
			if strings.HasPrefix(principalID, ad.UserScope+"://") {
				migratableUsers[principalID] = &user
			}
		}
	}

	return migratableUsers, nil
}

func GetUserBindings(c *client.RancherClient) (map[string][]PrincipalIDResource, error) {
	userBindings := map[string][]PrincipalIDResource{}

	prtbs := &apiv3.ProjectRoleTemplateBindingList{}
	err := c.Rancher.Get().Resource("projectroletemplatebindings").Do(context.Background()).Into(prtbs)
	if err != nil {
		return nil, err
	}

	for _, prtb := range prtbs.Items {
		if strings.HasPrefix(prtb.UserPrincipalName, ad.UserScope+"://") {
			bindings, found := userBindings[prtb.UserPrincipalName]
			if !found {
				bindings = []PrincipalIDResource{}
			}

			userBindings[prtb.UserPrincipalName] = append(bindings, &PRTBResource{
				PRTB: &prtb,
			})
		}
	}

	crtbs := &apiv3.ClusterRoleTemplateBindingList{}
	err = c.Rancher.Get().Resource("clusterroletemplatebindings").Do(context.Background()).Into(crtbs)
	if err != nil {
		return nil, err
	}

	for _, crtb := range crtbs.Items {
		if strings.HasPrefix(crtb.UserPrincipalName, ad.UserScope+"://") {
			bindings, found := userBindings[crtb.UserPrincipalName]
			if !found {
				bindings = []PrincipalIDResource{}
			}

			userBindings[crtb.UserPrincipalName] = append(bindings, &CRTBResource{
				CRTB: &crtb,
			})
		}
	}

	tokens := &apiv3.TokenList{}
	err = c.Rancher.Get().Resource("tokens").Do(context.Background()).Into(tokens)
	if err != nil {
		return nil, err
	}

	for _, token := range tokens.Items {
		if strings.HasPrefix(token.UserPrincipal.Name, ad.UserScope+"://") {
			bindings, found := userBindings[token.UserPrincipal.Name]
			if !found {
				bindings = []PrincipalIDResource{}
			}

			userBindings[token.UserPrincipal.Name] = append(bindings, &TokenResource{
				Token: &token,
			})
		}
	}

	return userBindings, nil
}

func getGUID(lConn *ldapv3.Conn, config *apiv3.ActiveDirectoryConfig, dn string) (guid.GUID, error) {
	search := ldap.NewBaseObjectSearchRequest(
		dn,
		fmt.Sprintf("(%v=%v)", ad.ObjectClass, config.UserObjectClass),
		config.GetUserSearchAttributes(ad.MemberOfAttribute, ad.ObjectClass, "objectGUID"),
	)

	results, err := lConn.Search(search)
	if err != nil {
		return nil, fmt.Errorf("LDAP search of user by objectGUID failed: %w", err)
	}

	objectGUID := results.Entries[0].GetRawAttributeValue("objectGUID")
	parsedGuid, err := guid.New(objectGUID)
	if err != nil {
		return nil, fmt.Errorf("LDAP search of user by objectGUID failed: %w", err)
	}

	return parsedGuid, nil
}

func getDN(lConn *ldapv3.Conn, config *apiv3.ActiveDirectoryConfig, uuid guid.GUID) (string, error) {
	filter := fmt.Sprintf(
		"(&(%v=%v)(%s=%s))",
		ad.ObjectClass, config.UserObjectClass,
		ad.ObjectGUIDAttribute, guid.Escape(uuid),
	)

	search := ldap.NewWholeSubtreeSearchRequest(
		config.UserSearchBase,
		filter,
		config.GetUserSearchAttributes(ad.MemberOfAttribute, ad.ObjectClass, "objectGUID"),
	)

	results, err := lConn.Search(search)
	if err != nil {
		return "", fmt.Errorf("LDAP search of user by objectGUID failed: %w", err)
	}

	return results.Entries[0].DN, nil
}

func UpdateResources(c *client.RancherClient, resources []*MigratableResource) error {
	var err error

	for i, res := range resources {
		ctx := context.Background()

		updatedPrincipalID := GetUpdatedPrincipalID(res)
		fmt.Printf("--- (%02d/%02d) ---\n", i+1, len(resources))
		fmt.Printf("Updating principal %s\nto %s\n", red(res.PrincipalID), green(updatedPrincipalID))

		// updating user principal
		if res.User != nil {
			fmt.Printf("- Updating user %s (%s) principal\n", blue(res.User.Name), blue(res.User.DisplayName))

			res.UpdatePrincipalID(updatedPrincipalID)

			result := c.Rancher.Put().Resource("users").Name(res.User.Name).Body(res.User).Do(ctx)
			err = result.Error()
			if err != nil {
				return err
			}

			fmt.Println("User updated")
		}

		prtbs := GetResourceByType[*PRTBResource](res.Bindings)
		if len(prtbs) == 0 {
			fmt.Println("- No ProjectRoleTemplateBindings to update.")
		} else {
			fmt.Printf("- Updating %d ProjectRoleTemplateBindings\n", len(prtbs))

			for _, prtb := range prtbs {
				// update PRTB
				prtb.SetPrincipalName(updatedPrincipalID)

				err = UpdatePRTB(ctx, c, prtb)
				if err != nil {
					return err
				}
			}
		}

		crtbs := GetResourceByType[*CRTBResource](res.Bindings)
		if len(crtbs) == 0 {
			fmt.Println("- No ClusterRoleTemplateBindings to update.")
		} else {
			fmt.Printf("- Updating %d ClusterRoleTemplateBindings\n", len(crtbs))

			for _, crtb := range crtbs {
				// update CRTB
				crtb.SetPrincipalName(updatedPrincipalID)

				err = UpdateCRTB(ctx, c, crtb)
				if err != nil {
					return err
				}
			}
		}

		tokens := GetResourceByType[*TokenResource](res.Bindings)
		if len(tokens) == 0 {
			fmt.Println("- No Tokens to update.")
		} else {
			fmt.Printf("- Updating %d Tokens\n", len(crtbs))

			for _, token := range tokens {
				// update Token
				token.SetPrincipalName(updatedPrincipalID)

				err = UpdateToken(ctx, c, token)
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func GetUpdatedPrincipalID(u *MigratableResource) string {
	if !strings.Contains(u.PrincipalID, ad.ObjectGUIDAttribute) {
		return fmt.Sprintf(
			"%s://%s=%s",
			ad.UserScope, ad.ObjectGUIDAttribute, u.GUID.UUID(),
		)
	}

	return fmt.Sprintf("%s://%s", ad.UserScope, u.DN)
}

func UpdatePRTB(ctx context.Context, c *client.RancherClient, prtb *PRTBResource) error {
	// generate a new PRTB
	oldPRTBName := prtb.PRTB.Name
	prtb.PRTB.Name = ""
	prtb.PRTB.ResourceVersion = ""

	fmt.Printf("Creating new ProjectRoleTemplateBinding in namespace %s\n", yellow(prtb.PRTB.Namespace))

	newPRTB := &apiv3.ProjectRoleTemplateBinding{}
	err := c.Rancher.Post().Resource("projectroletemplatebindings").
		Namespace(prtb.PRTB.Namespace).
		Body(prtb.PRTB).
		Do(ctx).
		Into(newPRTB)
	if err != nil {
		return fmt.Errorf("cannot create new ProjectRoleTemplateBinding: %w\n", err)
	}

	fmt.Printf(
		"- New ProjectRoleTemplateBinding '%s' in namespace '%s' created.\n",
		green(newPRTB.Name), yellow(newPRTB.Namespace),
	)

	fmt.Printf(
		"Deleting old ProjectRoleTemplateBinding '%s' in namespace '%s'\n",
		red(oldPRTBName), yellow(prtb.PRTB.Namespace),
	)
	err = c.Rancher.Delete().Resource("projectroletemplatebindings").
		Name(oldPRTBName).
		Namespace(prtb.PRTB.Namespace).
		Do(ctx).
		Error()
	if err != nil {
		return fmt.Errorf("cannot delete old ProjectRoleTemplateBinding: %w", err)
	}

	fmt.Printf(
		"- Old ProjectRoleTemplateBinding '%s' in namespace '%s' deleted\n",
		red(oldPRTBName), yellow(prtb.PRTB.Namespace),
	)
	return nil
}

func UpdateCRTB(ctx context.Context, c *client.RancherClient, crtb *CRTBResource) error {
	// generate a new CRTB
	oldCRTBName := crtb.CRTB.Name
	crtb.CRTB.Name = ""
	crtb.CRTB.ResourceVersion = ""

	fmt.Printf("Creating new ClusterRoleTemplateBinding in namespace %s\n", blue(crtb.CRTB.Namespace))

	newCRTB := &apiv3.ClusterRoleTemplateBinding{}
	err := c.Rancher.Post().Resource("clusterroletemplatebindings").
		Namespace(crtb.CRTB.Namespace).
		Body(crtb.CRTB).
		Do(ctx).
		Into(newCRTB)
	if err != nil {
		return fmt.Errorf(
			"cannot create new ClusterRoleTemplateBinding in namespace '%s': %w\n",
			crtb.CRTB.Namespace, err,
		)
	}

	fmt.Printf(
		"New ClusterRoleTemplateBinding created (%s), deleting old one (%s)\n",
		green(newCRTB.Name),
		red(oldCRTBName),
	)

	err = c.Rancher.Delete().Resource("clusterroletemplatebindings").
		Name(oldCRTBName).
		Namespace(crtb.CRTB.Namespace).
		Do(ctx).
		Error()
	if err != nil {
		return fmt.Errorf(
			"cannot delete old ClusterRoleTemplateBinding '%s' in namespace '%s': %w\n",
			oldCRTBName, crtb.CRTB.Namespace, err,
		)
	}

	fmt.Printf("Old ClusterRoleTemplateBinding deleted (%s)\n", red(oldCRTBName))
	return nil
}

func UpdateToken(ctx context.Context, c *client.RancherClient, token *TokenResource) error {
	err := c.Rancher.Put().Resource("tokens").
		Name(token.Token.Name).
		Body(token.Token).
		Do(ctx).
		Error()
	if err != nil {
		return fmt.Errorf("cannot update token '%s': %w\n", token.Token.Name, err)
	}

	fmt.Printf("Token updated (%s)\n", green(token.Token.Name))
	return nil
}
