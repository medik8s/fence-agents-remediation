// Code generated by applyconfiguration-gen. DO NOT EDIT.

package v1

// ProxyStatusApplyConfiguration represents an declarative configuration of the ProxyStatus type for use
// with apply.
type ProxyStatusApplyConfiguration struct {
	HTTPProxy  *string `json:"httpProxy,omitempty"`
	HTTPSProxy *string `json:"httpsProxy,omitempty"`
	NoProxy    *string `json:"noProxy,omitempty"`
}

// ProxyStatusApplyConfiguration constructs an declarative configuration of the ProxyStatus type for use with
// apply.
func ProxyStatus() *ProxyStatusApplyConfiguration {
	return &ProxyStatusApplyConfiguration{}
}

// WithHTTPProxy sets the HTTPProxy field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the HTTPProxy field is set to the value of the last call.
func (b *ProxyStatusApplyConfiguration) WithHTTPProxy(value string) *ProxyStatusApplyConfiguration {
	b.HTTPProxy = &value
	return b
}

// WithHTTPSProxy sets the HTTPSProxy field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the HTTPSProxy field is set to the value of the last call.
func (b *ProxyStatusApplyConfiguration) WithHTTPSProxy(value string) *ProxyStatusApplyConfiguration {
	b.HTTPSProxy = &value
	return b
}

// WithNoProxy sets the NoProxy field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the NoProxy field is set to the value of the last call.
func (b *ProxyStatusApplyConfiguration) WithNoProxy(value string) *ProxyStatusApplyConfiguration {
	b.NoProxy = &value
	return b
}
