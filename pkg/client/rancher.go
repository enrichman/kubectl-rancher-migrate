package client

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	apiv3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
)

type RancherClient struct {
	Kube    kubernetes.Interface
	Rancher rest.Interface
}

func NewRancherClient(config *rest.Config) (*RancherClient, error) {
	rancherScheme := runtime.NewScheme()
	err := apiv3.AddToScheme(rancherScheme)
	if err != nil {
		return nil, err
	}

	config.NegotiatedSerializer = serializer.NewCodecFactory(rancherScheme)
	config.APIPath = "/apis"
	config.ContentConfig.GroupVersion = &apiv3.SchemeGroupVersion

	k8sClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	restClient, err := rest.UnversionedRESTClientFor(config)
	if err != nil {
		return nil, err
	}

	return &RancherClient{
		Kube:    k8sClient,
		Rancher: restClient,
	}, nil
}
