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
			existingDescriptor.UnreachableAt = nil
			existingDescriptor.UnreachableCount = 0
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

func (r *GatewayServiceIndexer) MarkReachable(name string, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return
	}

	for _, sd := range r.ServiceDescriptors {
		if sd.Name == name {
			sd.LastSeenAt = &now
			sd.UnreachableAt = nil
			sd.UnreachableCount = 0
			return
		}
	}
}

func (r *GatewayServiceIndexer) PruneStaleServices(staleThreshold time.Duration, removeThreshold time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return
	}

	now := time.Now()
	remaining := r.ServiceDescriptors[:0]

	for _, sd := range r.ServiceDescriptors {
		if sd.LastSeenAt != nil && now.Sub(*sd.LastSeenAt) > staleThreshold && sd.UnreachableAt == nil {
			gatewayIndexerDebug.Tracef("Service %s is stale, marking unreachable", sd.Name)
			sd.UnreachableAt = &now
			sd.UnreachableCount++
		}

		if sd.UnreachableAt != nil && now.Sub(*sd.UnreachableAt) > removeThreshold {
			gatewayIndexerDebug.Tracef("Removing stale service %s", sd.Name)
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
		if remoteService.UnreachableAt != nil {
			continue
		}
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
