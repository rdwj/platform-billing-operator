/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"sync"

	"github.com/rdwj/platform-billing-operator/internal/psp"
)

// BackendRegistry is a thread-safe in-process map of connector name to
// PSPBackend instance. The BillingConnector controller populates it;
// other controllers (e.g. TenantAccount) look up backends by name.
type BackendRegistry struct {
	mu       sync.RWMutex
	backends map[string]psp.PSPBackend
}

// NewBackendRegistry creates an empty BackendRegistry.
func NewBackendRegistry() *BackendRegistry {
	return &BackendRegistry{backends: make(map[string]psp.PSPBackend)}
}

// Get returns the PSPBackend registered under name, or (nil, false) if absent.
func (r *BackendRegistry) Get(name string) (psp.PSPBackend, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	b, ok := r.backends[name]
	return b, ok
}

// Set registers (or replaces) a PSPBackend under the given name.
func (r *BackendRegistry) Set(name string, b psp.PSPBackend) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.backends[name] = b
}

// Delete removes the PSPBackend registered under name (no-op if absent).
func (r *BackendRegistry) Delete(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.backends, name)
}
