// package version_1_10_0 Handle the migration to v1.10.0
package version_1_10_0

import (
	"context"
	"fmt"
	"slices"
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

type UserToMigrate struct {
	User     *apiv3.User
	DN       string
	GUID     guid.GUID
	Bindings []PrincipalIDResource
}

func (u *UserToMigrate) GetActiveDirectoryPrincipalID() (string, bool) {
	for _, principalID := range u.User.PrincipalIDs {
		if strings.HasPrefix(principalID, ad.UserScope+"://") {
			return principalID, true
		}
	}
	return "", false
}

func (u *UserToMigrate) UpdatePrincipalID(orig, updated string) bool {
	for i, principalID := range u.User.PrincipalIDs {
		if orig == principalID {
			u.User.PrincipalIDs[i] = updated
			return true
		}
	}
	return false
}

func (u *UserToMigrate) GetBindings() ([]*PRTBResource, []*CRTBResource) {
	prtbs, crtsb := []*PRTBResource{}, []*CRTBResource{}

	for _, binding := range u.Bindings {
		switch b := binding.(type) {
		case *PRTBResource:
			prtbs = append(prtbs, b)
		case *CRTBResource:
			crtsb = append(crtsb, b)
		}
	}

	return prtbs, crtsb
}

func Check(c *client.RancherClient, lConn *client.LdapClient, config *apiv3.ActiveDirectoryConfig) error {
	fmt.Println("Check")

	users, err := GetUsersToMigrate(c)
	if err != nil {
		return err
	}

	fmt.Printf("Found %d users to migrate:\n", len(users))

	for _, u := range users {
		if err := setGUID(lConn.Conn, config, u); err != nil {
			return err
		}

		prtbs, crtbs := u.GetBindings()

		fmt.Printf("- %-15q %-20q - %-55s -> %s\n", u.User.Name, u.User.DisplayName, u.DN, u.GUID.UUID())
		fmt.Printf("\tUser bindings: %d PRTB, %d CRTB\n", len(prtbs), len(crtbs))
	}

	return nil
}

func Migrate(c *client.RancherClient, lConn *client.LdapClient, config *apiv3.ActiveDirectoryConfig, userIDs []string) error {
	fmt.Println("Start migration")

	users, err := GetUsersToMigrate(c)
	if err != nil {
		return err
	}

	for _, user := range users {
		// if userIDs is not empty filter users to be migrated
		if len(userIDs) > 0 && !slices.Contains(userIDs, user.User.Name) {
			continue
		}

		fmt.Printf("Migrating user %q (%q)\n", user.User.DisplayName, user.User.Name)

		if err := setGUID(lConn.Conn, config, user); err != nil {
			return err
		}

		adPrincipalID, found := user.GetActiveDirectoryPrincipalID()
		if !found {
			// continue (not AD user)
			continue
		}

		updatedPrincipalID := fmt.Sprintf(
			"%s://%s=%s",
			ad.UserScope, ad.ObjectGUIDAttribute, user.GUID.UUID(),
		)

		// updating user principal
		fmt.Printf("\t%s\n\t%s\n", red(adPrincipalID), green(updatedPrincipalID))
		user.UpdatePrincipalID(adPrincipalID, updatedPrincipalID)

		result := c.Rancher.Put().Resource("users").Name(user.User.Name).Body(user.User).Do(context.Background())
		err = result.Error()
		if err != nil {
			return err
		}

		prtbs, crtbs := user.GetBindings()

		// update PRTBs
		for _, prtb := range prtbs {
			prtb.SetPrincipalName(updatedPrincipalID)

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
		}

		// update CRTBs
		for _, crtb := range crtbs {
			crtb.SetPrincipalName(updatedPrincipalID)

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
		}
	}

	return err
}

func Rollback(c *client.RancherClient, lConn *client.LdapClient, config *apiv3.ActiveDirectoryConfig) error {
	fmt.Println("Start rollback")

	users, err := GetMigratedUsers(c)
	if err != nil {
		return err
	}

	for _, user := range users {
		fmt.Printf("Rolling back user %q (%q)\n", user.User.DisplayName, user.User.Name)

		if err := setDN(lConn.Conn, config, user); err != nil {
			return err
		}

		for i, principalID := range user.User.PrincipalIDs {
			if strings.HasPrefix(principalID, ad.UserScope+"://") {
				updatedPrincipalID := fmt.Sprintf("%s://%s", ad.UserScope, user.DN)

				fmt.Printf("\t%s\n\t%s\n", red(principalID), green(updatedPrincipalID))

				user.User.PrincipalIDs[i] = updatedPrincipalID
				break
			}
		}

		result := c.Rancher.Put().Resource("users").Name(user.User.Name).Body(user.User).Do(context.Background())
		err = result.Error()
		if err != nil {
			return err
		}
	}

	return nil
}

// GetUsersToMigrate will fetch all the users with an old activedirectory PrincipalID
func GetUsersToMigrate(c *client.RancherClient) ([]*UserToMigrate, error) {
	users := &apiv3.UserList{}
	err := c.Rancher.Get().Resource("users").Do(context.Background()).Into(users)
	if err != nil {
		return nil, err
	}

	usersToMigrate := []*UserToMigrate{}

	for _, user := range users.Items {
		for _, principalID := range user.PrincipalIDs {
			if strings.HasPrefix(principalID, ad.UserScope+"://") {
				userDN := strings.TrimPrefix(principalID, ad.UserScope+"://")

				parsedDN, err := ldapv3.ParseDN(userDN)
				if err != nil {
					return nil, err
				}

				// validate DN
				var foundDN bool
				for _, attr := range parsedDN.RDNs[0].Attributes {
					if attr.Type == "CN" {
						foundDN = true
						break
					}
				}

				if !foundDN {
					continue
				}

				usersToMigrate = append(usersToMigrate, &UserToMigrate{
					User: &user,
					DN:   userDN,
				})
			}
		}
	}

	userBindings, err := GetUserBindings(c)
	if err != nil {
		return nil, err
	}

	userResourcesMap := map[string][]PrincipalIDResource{}

	// split resources for users
	for _, binding := range userBindings {
		key := binding.GetUserPrincipalName()
		if _, found := userResourcesMap[key]; !found {
			userResourcesMap[key] = []PrincipalIDResource{}
		}
		userResourcesMap[key] = append(userResourcesMap[key], binding)
	}

	// assign bindings to users
	for _, u := range usersToMigrate {
		principalID, _ := u.GetActiveDirectoryPrincipalID()
		if _, found := userResourcesMap[principalID]; found {
			u.Bindings = userResourcesMap[principalID]
		}
	}

	return usersToMigrate, nil
}

func GetMigratedUsers(c *client.RancherClient) ([]*UserToMigrate, error) {
	users := &apiv3.UserList{}
	err := c.Rancher.Get().Resource("users").Do(context.Background()).Into(users)
	if err != nil {
		return nil, err
	}

	usersToMigrate := []*UserToMigrate{}

	for _, user := range users.Items {
		for _, principalID := range user.PrincipalIDs {

			objectGUIDPrincipalPrefix := fmt.Sprintf("%s://%s=", ad.UserScope, ad.ObjectGUIDAttribute)
			if strings.HasPrefix(principalID, objectGUIDPrincipalPrefix) {
				objectGUID := strings.TrimPrefix(principalID, objectGUIDPrincipalPrefix)

				parsedGUID, err := guid.Parse(objectGUID)
				if err != nil {
					return nil, err
				}

				usersToMigrate = append(usersToMigrate, &UserToMigrate{
					User: &user,
					GUID: parsedGUID,
				})
			}
		}
	}

	return usersToMigrate, nil
}

func GetUserBindings(c *client.RancherClient) ([]PrincipalIDResource, error) {
	userBindings := []PrincipalIDResource{}

	prtbs := &apiv3.ProjectRoleTemplateBindingList{}
	err := c.Rancher.Get().Resource("projectroletemplatebindings").Do(context.Background()).Into(prtbs)
	if err != nil {
		return nil, err
	}

	for _, prtb := range prtbs.Items {
		userBindings = append(userBindings, &PRTBResource{
			PRTB: &prtb,
		})
	}

	crtbs := &apiv3.ClusterRoleTemplateBindingList{}
	err = c.Rancher.Get().Resource("clusterroletemplatebindings").Do(context.Background()).Into(crtbs)
	if err != nil {
		return nil, err
	}

	for _, crtb := range crtbs.Items {
		userBindings = append(userBindings, &CRTBResource{
			CRTB: &crtb,
		})
	}

	return userBindings, nil
}

func setGUID(lConn *ldapv3.Conn, config *apiv3.ActiveDirectoryConfig, user *UserToMigrate) error {
	search := ldap.NewBaseObjectSearchRequest(
		user.DN,
		fmt.Sprintf("(%v=%v)", ad.ObjectClass, config.UserObjectClass),
		config.GetUserSearchAttributes(ad.MemberOfAttribute, ad.ObjectClass, "objectGUID"),
	)

	results, err := lConn.Search(search)
	if err != nil {
		return fmt.Errorf("LDAP search of user by objectGUID failed: %w", err)
	}

	objectGUID := results.Entries[0].GetRawAttributeValue("objectGUID")
	parsedGuid, err := guid.New(objectGUID)
	if err != nil {
		return fmt.Errorf("LDAP search of user by objectGUID failed: %w", err)
	}

	user.GUID = parsedGuid
	return nil
}

func setDN(lConn *ldapv3.Conn, config *apiv3.ActiveDirectoryConfig, user *UserToMigrate) error {
	filter := fmt.Sprintf(
		"(&(%v=%v)(%s=%s))",
		ad.ObjectClass, config.UserObjectClass,
		ad.ObjectGUIDAttribute, guid.Escape(user.GUID),
	)

	search := ldap.NewWholeSubtreeSearchRequest(
		config.UserSearchBase,
		filter,
		config.GetUserSearchAttributes(ad.MemberOfAttribute, ad.ObjectClass, "objectGUID"),
	)

	results, err := lConn.Search(search)
	if err != nil {
		return fmt.Errorf("LDAP search of user by objectGUID failed: %w", err)
	}

	user.DN = results.Entries[0].DN
	return nil
}
