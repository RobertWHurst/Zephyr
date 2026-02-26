package zephyr

import "sync"

type GatewayServiceIndexer struct {
	mu                 sync.Mutex
	ServiceDescriptors []*ServiceDescriptor
}

func (r *GatewayServiceIndexer) SetServiceDescriptor(descriptor *ServiceDescriptor) error {
	r.mu.Lock()
	defer r.mu.Unlock()
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
	for i, service := range r.ServiceDescriptors {
		if service.Name == name {
			r.ServiceDescriptors = append(r.ServiceDescriptors[:i], r.ServiceDescriptors[i+1:]...)
			return nil
		}
	}
	return nil
}

func (r *GatewayServiceIndexer) ResolveService(method string, path string) (string, *RouteDescriptor, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, remoteService := range r.ServiceDescriptors {
		if remoteService.UnreachableAt != nil {
			continue
		}
		for _, httpRoute := range remoteService.RouteDescriptors {
			if httpRoute.Method != method {
				continue
			}
			if _, isMatch := httpRoute.Pattern.Match(path); isMatch {
				return remoteService.Name, httpRoute, true
			}
		}
	}
	return "", nil, false
}
