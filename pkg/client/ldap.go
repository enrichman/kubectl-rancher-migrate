package client

import (
	"context"
	"crypto/x509"
	"strings"

	ldapv3 "github.com/go-ldap/ldap/v3"
	apiv3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/rancher/pkg/auth/providers/common/ldap"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	typedv1 "k8s.io/client-go/kubernetes/typed/core/v1"
)

type LdapClient struct {
	*ldapv3.Conn
}

type LDAPConfig struct {
	Servers                []string
	TLS                    bool
	StartTLS               bool
	Port                   int64
	ConnectionTimeout      int64
	CAPool                 *x509.CertPool
	ServiceAccountName     string
	ServiceAccountPassword string
	DefaultLoginDomain     string
}

func NewLDAPConfigFromActiveDirectory(core typedv1.CoreV1Interface, config *apiv3.ActiveDirectoryConfig) (*LDAPConfig, error) {
	caPool, err := x509.SystemCertPool()
	if err != nil {
		return nil, err
	}

	if config.Certificate != "" {
		caPool.AppendCertsFromPEM([]byte(config.Certificate))
	}

	serviceAccountPassword := getServiceAccountPassword(core, config.ServiceAccountPassword)

	return &LDAPConfig{
		Servers:                config.Servers,
		TLS:                    config.TLS,
		StartTLS:               config.StartTLS,
		Port:                   config.Port,
		ConnectionTimeout:      config.ConnectionTimeout,
		CAPool:                 caPool,
		ServiceAccountName:     config.ServiceAccountUsername,
		ServiceAccountPassword: serviceAccountPassword,
		DefaultLoginDomain:     config.DefaultLoginDomain,
	}, nil
}

func NewLDAPConn(config *LDAPConfig) (*ldapv3.Conn, error) {
	lConn, err := ldap.NewLDAPConn(
		config.Servers,
		config.TLS,
		config.StartTLS,
		config.Port,
		config.ConnectionTimeout,
		config.CAPool,
	)
	if err != nil {
		return nil, err
	}

	err = ldap.AuthenticateServiceAccountUser(
		config.ServiceAccountPassword,
		config.ServiceAccountName,
		config.DefaultLoginDomain,
		lConn,
	)
	if err != nil {
		return nil, err
	}

	return lConn, nil
}

func getServiceAccountPassword(core typedv1.CoreV1Interface, serviceAccountPassword string) string {
	namespaceAndName := strings.Split(serviceAccountPassword, ":")
	if len(namespaceAndName) < 2 {
		return serviceAccountPassword
	}

	secretNamespace, secretName := namespaceAndName[0], namespaceAndName[1]

	sec, err := core.Secrets(secretNamespace).Get(context.Background(), secretName, v1.GetOptions{})
	if err != nil {
		return serviceAccountPassword
	}
	return string(sec.Data["serviceaccountpassword"])
}
