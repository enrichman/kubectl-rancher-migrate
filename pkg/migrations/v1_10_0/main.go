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
	blue  = color.New(color.FgBlue).SprintFunc()
	green = color.New(color.FgGreen).SprintFunc()
	red   = color.New(color.FgRed).SprintFunc()
)

func Check(c *client.RancherClient, lConn *client.LdapClient, config *apiv3.ActiveDirectoryConfig) error {
	fmt.Println("Check")

	migratable, err := GetMigratableResources(c, lConn.Conn, config)
	if err != nil {
		return err
	}

	fmt.Printf("Found %d users to migrate:\n", len(migratable))

	for pID, res := range migratable {
		fmt.Printf("- %s\n", blue(pID))
		fmt.Printf("\tGUID:\t%s\n", green(res.GUID.UUID()))
		fmt.Printf("\tDN:\t%s\n", red(res.DN))

		var prtbs, crtbs int

		for _, bind := range res.Bindings {
			switch bind.(type) {
			case *PRTBResource:
				prtbs++
			case *CRTBResource:
				crtbs++
			}
		}

		fmt.Printf("\tPRTBs: %d, CRTBs: %d\n", prtbs, prtbs)
	}

	fmt.Println("DNSSS")
	for pID, res := range migratable.WithDNs() {
		fmt.Printf("- %s\n", blue(pID))
		fmt.Printf("\tGUID:\t%s\n", green(res.GUID.UUID()))
		fmt.Printf("\tDN:\t%s\n", red(res.DN))

		var prtbs, crtbs int

		for _, bind := range res.Bindings {
			switch bind.(type) {
			case *PRTBResource:
				prtbs++
			case *CRTBResource:
				crtbs++
			}
		}

		fmt.Printf("\tPRTBs: %d, CRTBs: %d\n", prtbs, prtbs)
	}

	fmt.Println("GUIDSS")
	for pID, res := range migratable.WithGUIDs() {
		fmt.Printf("- %s\n", blue(pID))
		fmt.Printf("\tGUID:\t%s\n", green(res.GUID.UUID()))
		fmt.Printf("\tDN:\t%s\n", red(res.DN))

		var prtbs, crtbs int

		for _, bind := range res.Bindings {
			switch bind.(type) {
			case *PRTBResource:
				prtbs++
			case *CRTBResource:
				crtbs++
			}
		}

		fmt.Printf("\tPRTBs: %d, CRTBs: %d\n", prtbs, prtbs)
	}

	return nil
}

func Migrate(c *client.RancherClient, lConn *client.LdapClient, config *apiv3.ActiveDirectoryConfig, userIDs []string) error {
	fmt.Println("Start migration")

	migratable, err := GetMigratableResources(c, lConn.Conn, config)
	if err != nil {
		return err
	}
	dnResources := migratable.WithDNs()

	return updateResources(c, dnResources)
}

func Rollback(c *client.RancherClient, lConn *client.LdapClient, config *apiv3.ActiveDirectoryConfig, userIDs []string) error {
	fmt.Println("Start rollback")

	migratable, err := GetMigratableResources(c, lConn.Conn, config)
	if err != nil {
		return err
	}
	guidResources := migratable.WithGUIDs()

	return updateResources(c, guidResources)
}

// GetMigratableResources
func GetMigratableResources(c *client.RancherClient, lConn *ldapv3.Conn, config *apiv3.ActiveDirectoryConfig) (MigratableResources, error) {
	resourcesToMigrate := map[string]*MigratableResource{}

	userMap, err := GetUsersToMigrate(c)
	if err != nil {
		return nil, err
	}

	for principalID, user := range userMap {
		res := &MigratableResource{
			PrincipalID: principalID,
			User:        user,
		}

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

		resourcesToMigrate[principalID] = res
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

			objectGUIDPrincipalPrefix := fmt.Sprintf("%s://%s=", ad.UserScope, ad.ObjectGUIDAttribute)
			if strings.HasPrefix(principalID, objectGUIDPrincipalPrefix) {
				objectGUID := strings.TrimPrefix(principalID, objectGUIDPrincipalPrefix)

				parsedGUID, err := guid.Parse(objectGUID)
				if err != nil {
					return nil, err
				}

				res.GUID = parsedGUID
			} else {
				dn := strings.TrimPrefix(principalID, ad.UserScope+"://")
				res.DN = dn
			}
		}

		res.Bindings = binds

		resourcesToMigrate[principalID] = res
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

func UpdatePRTB(c *client.RancherClient, prtb *PRTBResource) error {
	// generate a new PRTB
	oldPRTBName := prtb.PRTB.Name
	prtb.PRTB.Name = ""
	prtb.PRTB.ResourceVersion = ""

	fmt.Printf("\tCreating new ProjectRoleTemplateBinding in namespace %s\n", blue(prtb.PRTB.Namespace))

	newPRTB := &apiv3.ProjectRoleTemplateBinding{}
	err := c.Rancher.Post().Resource("projectroletemplatebindings").
		Namespace(prtb.PRTB.Namespace).
		Body(prtb.PRTB).
		Do(context.Background()).
		Into(newPRTB)
	if err != nil {
		return err
	}

	fmt.Printf(
		"\tNew ProjectRoleTemplateBinding created (%s), deleting old one (%s)\n",
		green(newPRTB.Name), red(oldPRTBName),
	)

	err = c.Rancher.Delete().Resource("projectroletemplatebindings").
		Name(oldPRTBName).
		Namespace(prtb.PRTB.Namespace).
		Do(context.Background()).
		Error()
	if err != nil {
		return err
	}

	fmt.Printf("\tOld ProjectRoleTemplateBinding deleted (%s)\n", red(oldPRTBName))
	return nil
}

func UpdateCRTB(c *client.RancherClient, crtb *CRTBResource) error {
	// generate a new CRTB
	oldCRTBName := crtb.CRTB.Name
	crtb.CRTB.Name = ""
	crtb.CRTB.ResourceVersion = ""

	fmt.Printf("\tCreating new ClusterRoleTemplateBinding in namespace %s\n", blue(crtb.CRTB.Namespace))

	newCRTB := &apiv3.ClusterRoleTemplateBinding{}
	err := c.Rancher.Post().Resource("clusterroletemplatebindings").
		Namespace(crtb.CRTB.Namespace).
		Body(crtb.CRTB).
		Do(context.Background()).
		Into(newCRTB)
	if err != nil {
		return err
	}

	fmt.Printf(
		"\tNew ClusterRoleTemplateBinding created (%s), deleting old one (%s)\n",
		green(newCRTB.Name),
		red(oldCRTBName),
	)

	err = c.Rancher.Delete().Resource("clusterroletemplatebindings").
		Name(oldCRTBName).
		Namespace(crtb.CRTB.Namespace).
		Do(context.Background()).
		Error()
	if err != nil {
		return err
	}

	fmt.Printf("\tOld ClusterRoleTemplateBinding deleted (%s)\n", red(oldCRTBName))
	return nil
}

func updateResources(c *client.RancherClient, resources []*MigratableResource) error {
	var err error

	for _, res := range resources {

		updatedPrincipalID := res.GetNewPrincipalID()
		fmt.Printf("\t%s\n\t%s\n", red(res.PrincipalID), green(updatedPrincipalID))

		// updating user principal
		if res.User != nil {
			res.UpdatePrincipalID(updatedPrincipalID)

			result := c.Rancher.Put().Resource("users").Name(res.User.Name).Body(res.User).Do(context.Background())
			err = result.Error()
			if err != nil {
				return err
			}
		}

		for _, bind := range res.Bindings {
			switch b := bind.(type) {
			case *PRTBResource:
				// update PRTB
				b.SetPrincipalName(updatedPrincipalID)

				err = UpdatePRTB(c, b)
				if err != nil {
					return err
				}

			case *CRTBResource:
				// update PRTB
				b.SetPrincipalName(updatedPrincipalID)

				err = UpdateCRTB(c, b)
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}
