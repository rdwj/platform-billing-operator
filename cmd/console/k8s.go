package main

import (
	"fmt"
	"os"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func newK8sClient() (ctrlclient.Client, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		kubeconfig := os.Getenv("KUBECONFIG")
		if kubeconfig == "" {
			return nil, fmt.Errorf("not in cluster and KUBECONFIG not set: %w", err)
		}
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("building kubeconfig: %w", err)
		}
	}

	s := runtime.NewScheme()
	if err := billingv1alpha1.AddToScheme(s); err != nil {
		return nil, fmt.Errorf("adding billing scheme: %w", err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		return nil, fmt.Errorf("adding core scheme: %w", err)
	}
	if err := authorizationv1.AddToScheme(s); err != nil {
		return nil, fmt.Errorf("adding authorization scheme: %w", err)
	}

	c, err := ctrlclient.New(cfg, ctrlclient.Options{Scheme: s})
	if err != nil {
		return nil, fmt.Errorf("creating k8s client: %w", err)
	}

	return c, nil
}
