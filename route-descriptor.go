package zephyr

import (
	"github.com/RobertWHurst/navaros"
	"github.com/vmihailenco/msgpack/v5"
)

// RouteDescriptor defines a route this service can handle. A route is a
// HTTP method, and a path matching pattern. It is used by the zephyr gateway
// to determine which service to dispatch a request to.
type RouteDescriptor struct {
	Method   string
	Pattern  *navaros.Pattern
	Metadata any
}

type routeDescriptorMsgpack struct {
	Method   string `msgpack:"Method"`
	Pattern  string `msgpack:"Pattern"`
	Metadata any    `msgpack:"Metadata,omitempty"`
}

// MarshalMsgpack returns the msgpack representation of the route descriptor.
func (r *RouteDescriptor) MarshalMsgpack() ([]byte, error) {
	return msgpack.Marshal(routeDescriptorMsgpack{
		Method:   r.Method,
		Pattern:  r.Pattern.String(),
		Metadata: r.Metadata,
	})
}

// UnmarshalMsgpack parses the msgpack representation of the route descriptor.
func (r *RouteDescriptor) UnmarshalMsgpack(data []byte) error {
	var raw routeDescriptorMsgpack
	if err := msgpack.Unmarshal(data, &raw); err != nil {
		return err
	}

	pattern, err := navaros.NewPattern(raw.Pattern)
	if err != nil {
		return err
	}

	r.Method = raw.Method
	r.Pattern = pattern
	r.Metadata = raw.Metadata

	return nil
}

// NewRouteDescriptor creates a new RouteDescriptor from a method and a path
// pattern. The pattern determines which URL path this route will match.
//
// To understand the pattern syntax, see the [navaros package](https://github.com/RobertWHurst/Navaros?tab=readme-ov-file#route-patterns).
func NewRouteDescriptor(method string, patternStr string) (*RouteDescriptor, error) {
	pattern, err := navaros.NewPattern(patternStr)
	if err != nil {
		return nil, err
	}
	return &RouteDescriptor{
		Method:  method,
		Pattern: pattern,
	}, nil
}
