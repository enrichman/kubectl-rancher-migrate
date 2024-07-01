package version_1_10_0

import (
	"strings"

	apiv3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	ad "github.com/rancher/rancher/pkg/auth/providers/activedirectory"
	"github.com/rancher/rancher/pkg/auth/providers/activedirectory/guid"
)

type PrincipalIDResource interface {
	GetUserPrincipalName() string
	SetPrincipalName(string)
}

type CRTBResource struct {
	CRTB *apiv3.ClusterRoleTemplateBinding
}

func (c *CRTBResource) GetUserPrincipalName() string {
	return c.CRTB.UserPrincipalName
}

func (c *CRTBResource) SetPrincipalName(principalName string) {
	c.CRTB.UserPrincipalName = principalName
}

type PRTBResource struct {
	PRTB *apiv3.ProjectRoleTemplateBinding
}

func (c *PRTBResource) GetUserPrincipalName() string {
	return c.PRTB.UserPrincipalName
}

func (c *PRTBResource) SetPrincipalName(principalName string) {
	c.PRTB.UserPrincipalName = principalName
}

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
