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
	green = color.New(color.FgGreen).SprintFunc()
	red   = color.New(color.FgRed).SprintFunc()
)

type UserToMigrate struct {
	User       *apiv3.User
	OriginalDN string
	GUID       guid.GUID
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

		fmt.Printf("- %-15q %-20q - %-55s -> %s\n", u.User.Name, u.User.DisplayName, u.OriginalDN, u.GUID.UUID())
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

		for i, principalID := range user.User.PrincipalIDs {
			if strings.HasPrefix(principalID, ad.UserScope+"://") {
				updatedPrincipalID := fmt.Sprintf(
					"%s://%s=%s",
					ad.UserScope, ad.ObjectGUIDAttribute, user.GUID.UUID(),
				)

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
				updatedPrincipalID := fmt.Sprintf("%s://%s", ad.UserScope, user.OriginalDN)

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
					User:       &user,
					OriginalDN: userDN,
				})
			}
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

func setGUID(lConn *ldapv3.Conn, config *apiv3.ActiveDirectoryConfig, user *UserToMigrate) error {
	search := ldap.NewBaseObjectSearchRequest(
		user.OriginalDN,
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

	user.OriginalDN = results.Entries[0].DN
	return nil
}
