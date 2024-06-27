package version_1_10_0

import (
	apiv3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
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
