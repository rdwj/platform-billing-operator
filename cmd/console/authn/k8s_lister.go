package authn

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const cacheTTL = 30 * time.Second

type K8sTenantLister struct {
	client    ctrlclient.Reader
	namespace string

	mu       sync.RWMutex
	cached   []TenantInfo
	cachedAt time.Time
}

func NewK8sTenantLister(namespace string) (*K8sTenantLister, error) {
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

	c, err := ctrlclient.New(cfg, ctrlclient.Options{Scheme: s})
	if err != nil {
		return nil, fmt.Errorf("creating k8s client: %w", err)
	}

	return &K8sTenantLister{
		client:    c,
		namespace: namespace,
	}, nil
}

func (l *K8sTenantLister) ListTenantAccounts(ctx context.Context) ([]TenantInfo, error) {
	l.mu.RLock()
	if time.Since(l.cachedAt) < cacheTTL && l.cached != nil {
		result := l.cached
		l.mu.RUnlock()
		return result, nil
	}
	l.mu.RUnlock()

	var list billingv1alpha1.TenantAccountList
	if err := l.client.List(ctx, &list, ctrlclient.InNamespace(l.namespace)); err != nil {
		return nil, fmt.Errorf("listing TenantAccounts in %s: %w", l.namespace, err)
	}

	infos := make([]TenantInfo, 0, len(list.Items))
	for _, ta := range list.Items {
		annotation := ta.Annotations[IdPGroupsAnnotation]
		groups := ParseIdPGroups(annotation)
		if len(groups) == 0 {
			continue
		}
		infos = append(infos, TenantInfo{
			Name:   ta.Name,
			Groups: groups,
		})
	}

	l.mu.Lock()
	l.cached = infos
	l.cachedAt = time.Now()
	l.mu.Unlock()

	return infos, nil
}
