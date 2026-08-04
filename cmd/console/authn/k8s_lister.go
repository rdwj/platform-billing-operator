package authn

import (
	"context"
	"fmt"
	"sync"
	"time"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
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

// NewK8sTenantLister creates a lister that watches TenantAccount CRs
// in the given namespace using the provided k8s client.
func NewK8sTenantLister(client ctrlclient.Reader, namespace string) *K8sTenantLister {
	return &K8sTenantLister{
		client:    client,
		namespace: namespace,
	}
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
