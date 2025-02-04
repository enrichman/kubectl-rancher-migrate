package version_1_10_0

import (
	"slices"
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

type TokenResource struct {
	Token *apiv3.Token
}

func (t *TokenResource) GetUserPrincipalName() string {
	return t.Token.UserPrincipal.Name
}

func (t *TokenResource) SetPrincipalName(principalName string) {
	t.Token.UserPrincipal.Name = principalName
}

type MigratableResources map[string]*MigratableResource

func (u MigratableResources) WithDNs() []*MigratableResource {
	var dns []*MigratableResource

	for k, v := range u {
		if !strings.Contains(k, ad.ObjectGUIDAttribute) {
			dns = append(dns, v)
		}
	}

	slices.SortFunc(dns, func(v1, v2 *MigratableResource) int {
		return strings.Compare(v1.DN, v2.DN)
	})

	return dns
}

func (u MigratableResources) WithGUIDs() []*MigratableResource {
	var uuids []*MigratableResource

	for k, v := range u {
		if strings.Contains(k, ad.ObjectGUIDAttribute) {
			uuids = append(uuids, v)
		}
	}

	slices.SortFunc(uuids, func(v1, v2 *MigratableResource) int {
		return strings.Compare(v1.GUID.String(), v2.GUID.String())
	})

	return uuids
}

type MigratableResource struct {
	User        *apiv3.User
	PrincipalID string
	DN          string
	GUID        guid.GUID
	Bindings    []PrincipalIDResource
}

func (u *MigratableResource) UpdatePrincipalID(updated string) bool {
	for i, principalID := range u.User.PrincipalIDs {
		if u.PrincipalID == principalID {
			u.User.PrincipalIDs[i] = updated
			return true
		}
	}
	return false
}

func (u *MigratableResource) GetBindings() ([]*PRTBResource, []*CRTBResource) {
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

func GetResourceByType[T PrincipalIDResource](resources []PrincipalIDResource) []T {
	var filtered []T

	for _, res := range resources {
		if t, ok := res.(T); ok {
			filtered = append(filtered, t)
		}
	}

	return filtered
}
