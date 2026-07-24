package zephyr

import (
	"github.com/RobertWHurst/navaros"
	"github.com/vmihailenco/msgpack/v5"
)

// RouteDescriptor defines a route this service can handle. A route is a
// HTTP method, and a path matching pattern. It is used by the zephyr gateway
// to determine which service to dispatch a request to.
type RouteDescriptor struct {
	Method  string
	Pattern *navaros.Pattern

	// Metadata carries arbitrary route metadata declared at the bind site
	// (for example with navaros.WithMetadata). After a msgpack round trip it
	// holds a msgpack.RawMessage; use UnmarshalMetadata to decode it into a
	// typed value regardless of how the descriptor was obtained.
	Metadata any
}

// UnmarshalMetadata decodes the route's metadata into the given value. It
// works both for descriptors received over a transport (where the metadata
// is a raw msgpack value) and for locally constructed descriptors (where it
// is the original value), so consumers behave identically across transports.
// Returns ErrNoRouteMetadata when the route has no metadata.
func (r *RouteDescriptor) UnmarshalMetadata(into any) error {
	if r.Metadata == nil {
		return ErrNoRouteMetadata
	}
	raw, ok := r.Metadata.(msgpack.RawMessage)
	if !ok {
		encoded, err := msgpack.Marshal(r.Metadata)
		if err != nil {
			return err
		}
		raw = encoded
	}
	return msgpack.Unmarshal(raw, into)
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

type routeDescriptorMsgpackRaw struct {
	Method   string             `msgpack:"Method"`
	Pattern  string             `msgpack:"Pattern"`
	Metadata msgpack.RawMessage `msgpack:"Metadata,omitempty"`
}

// UnmarshalMsgpack parses the msgpack representation of the route descriptor.
// Metadata is preserved as a raw msgpack value so consumers can decode it
// into their own types with UnmarshalMetadata.
func (r *RouteDescriptor) UnmarshalMsgpack(data []byte) error {
	var raw routeDescriptorMsgpackRaw
	if err := msgpack.Unmarshal(data, &raw); err != nil {
		return err
	}

	pattern, err := navaros.NewPattern(raw.Pattern)
	if err != nil {
		return err
	}

	r.Method = raw.Method
	r.Pattern = pattern
	if len(raw.Metadata) > 0 {
		r.Metadata = raw.Metadata
	} else {
		r.Metadata = nil
	}

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
