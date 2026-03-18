package zephyr

import "sync"

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

	for _, existingDescriptor := range r.ServiceDescriptors {
		if existingDescriptor.Name == descriptor.Name {
			existingDescriptor.RouteDescriptors = descriptor.RouteDescriptors
			return nil
		}
	}
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
