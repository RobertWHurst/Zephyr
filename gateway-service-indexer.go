package zephyr

import (
	"sync"
	"time"
)

type GatewayServiceIndexer struct {
	mu                 sync.Mutex
	closed             bool
	ServiceDescriptors []*ServiceDescriptor
}

func (r *GatewayServiceIndexer) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	r.ServiceDescriptors = nil
}

func (r *GatewayServiceIndexer) IsClosed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}

func (r *GatewayServiceIndexer) SetServiceDescriptor(descriptor *ServiceDescriptor) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return nil
	}

	now := time.Now()

	for _, existingDescriptor := range r.ServiceDescriptors {
		if existingDescriptor.Name == descriptor.Name {
			existingDescriptor.RouteDescriptors = descriptor.RouteDescriptors
			existingDescriptor.LastSeenAt = &now
			return nil
		}
	}

	descriptor.LastSeenAt = &now
	r.ServiceDescriptors = append(r.ServiceDescriptors, descriptor)
	return nil
}

func (r *GatewayServiceIndexer) UnsetService(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return nil
	}

	for i, service := range r.ServiceDescriptors {
		if service.Name == name {
			r.ServiceDescriptors = append(r.ServiceDescriptors[:i], r.ServiceDescriptors[i+1:]...)
			return nil
		}
	}
	return nil
}

// FreshServiceDescriptors returns service descriptors seen within the given
// threshold. Used by the gateway announce loop to exclude stale services from
// announcements — stale services won't see themselves in the list and will
// re-announce if still alive.
func (r *GatewayServiceIndexer) FreshServiceDescriptors(threshold time.Duration) []*ServiceDescriptor {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return nil
	}

	now := time.Now()
	var fresh []*ServiceDescriptor
	for _, sd := range r.ServiceDescriptors {
		if sd.LastSeenAt != nil && now.Sub(*sd.LastSeenAt) <= threshold {
			fresh = append(fresh, sd)
		}
	}
	return fresh
}

// PruneStaleServices removes services that haven't announced within the given
// threshold. Services excluded from gateway announcements are prompted to
// re-announce; those that don't respond are assumed dead and removed.
func (r *GatewayServiceIndexer) PruneStaleServices(removeThreshold time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return
	}

	now := time.Now()
	remaining := r.ServiceDescriptors[:0]

	for _, sd := range r.ServiceDescriptors {
		if sd.LastSeenAt != nil && now.Sub(*sd.LastSeenAt) > removeThreshold {
			gatewayIndexerDebug.Tracef("Removing stale service %s (last seen %s ago)", sd.Name, now.Sub(*sd.LastSeenAt))
			continue
		}

		remaining = append(remaining, sd)
	}

	r.ServiceDescriptors = remaining
}

func (r *GatewayServiceIndexer) ResolveService(method string, path string) (*ServiceDescriptor, *RouteDescriptor, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return nil, nil, false
	}

	for _, remoteService := range r.ServiceDescriptors {
		for _, httpRoute := range remoteService.RouteDescriptors {
			if httpRoute.Method != method {
				continue
			}
			if _, isMatch := httpRoute.Pattern.Match(path); isMatch {
				return remoteService, httpRoute, true
			}
		}
	}
	return nil, nil, false
}
