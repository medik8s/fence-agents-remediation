// Code generated by applyconfiguration-gen. DO NOT EDIT.

package v1

// LDAPIdentityProviderApplyConfiguration represents an declarative configuration of the LDAPIdentityProvider type for use
// with apply.
type LDAPIdentityProviderApplyConfiguration struct {
	URL          *string                                   `json:"url,omitempty"`
	BindDN       *string                                   `json:"bindDN,omitempty"`
	BindPassword *SecretNameReferenceApplyConfiguration    `json:"bindPassword,omitempty"`
	Insecure     *bool                                     `json:"insecure,omitempty"`
	CA           *ConfigMapNameReferenceApplyConfiguration `json:"ca,omitempty"`
	Attributes   *LDAPAttributeMappingApplyConfiguration   `json:"attributes,omitempty"`
}

// LDAPIdentityProviderApplyConfiguration constructs an declarative configuration of the LDAPIdentityProvider type for use with
// apply.
func LDAPIdentityProvider() *LDAPIdentityProviderApplyConfiguration {
	return &LDAPIdentityProviderApplyConfiguration{}
}

// WithURL sets the URL field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the URL field is set to the value of the last call.
func (b *LDAPIdentityProviderApplyConfiguration) WithURL(value string) *LDAPIdentityProviderApplyConfiguration {
	b.URL = &value
	return b
}

// WithBindDN sets the BindDN field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the BindDN field is set to the value of the last call.
func (b *LDAPIdentityProviderApplyConfiguration) WithBindDN(value string) *LDAPIdentityProviderApplyConfiguration {
	b.BindDN = &value
	return b
}

// WithBindPassword sets the BindPassword field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the BindPassword field is set to the value of the last call.
func (b *LDAPIdentityProviderApplyConfiguration) WithBindPassword(value *SecretNameReferenceApplyConfiguration) *LDAPIdentityProviderApplyConfiguration {
	b.BindPassword = value
	return b
}

// WithInsecure sets the Insecure field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Insecure field is set to the value of the last call.
func (b *LDAPIdentityProviderApplyConfiguration) WithInsecure(value bool) *LDAPIdentityProviderApplyConfiguration {
	b.Insecure = &value
	return b
}

// WithCA sets the CA field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the CA field is set to the value of the last call.
func (b *LDAPIdentityProviderApplyConfiguration) WithCA(value *ConfigMapNameReferenceApplyConfiguration) *LDAPIdentityProviderApplyConfiguration {
	b.CA = value
	return b
}

// WithAttributes sets the Attributes field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Attributes field is set to the value of the last call.
func (b *LDAPIdentityProviderApplyConfiguration) WithAttributes(value *LDAPAttributeMappingApplyConfiguration) *LDAPIdentityProviderApplyConfiguration {
	b.Attributes = value
	return b
}
