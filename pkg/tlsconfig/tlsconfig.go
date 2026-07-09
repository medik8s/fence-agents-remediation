package tlsconfig

import (
	"context"
	"crypto/tls"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/crypto"
)

// CreateTLSOptsForServer creates TLS option functions for controller-runtime servers.
// On OpenShift: applies cluster TLS profile while preserving HTTP/2 control settings.
// On vanilla K8s: only sets HTTP/2 control, lets libraries handle TLS (old behavior).
func CreateTLSOptsForServer(ctx context.Context, k8sClient client.Client, disableHTTP2 bool) ([]func(*tls.Config), error) {
	clusterTLSConfig, err := getClusterTLSConfig(ctx, k8sClient)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster TLS config: %w", err)
	}

	var tlsOpts []func(*tls.Config)

	// Apply cluster TLS profile (OpenShift only)
	if clusterTLSConfig != nil {
		tlsOpts = append(tlsOpts, func(c *tls.Config) {
			c.MinVersion = clusterTLSConfig.MinVersion
			if len(clusterTLSConfig.CipherSuites) > 0 {
				c.CipherSuites = clusterTLSConfig.CipherSuites
			}
		})
	}

	// Apply HTTP/2 control (both OpenShift and vanilla K8s)
	if disableHTTP2 {
		tlsOpts = append(tlsOpts, func(c *tls.Config) {
			c.NextProtos = []string{"http/1.1"}
		})
	}

	return tlsOpts, nil
}

// getClusterTLSConfig fetches the cluster's TLS security profile from the OpenShift APIServer
// configuration and returns a tls.Config configured with the cluster's TLS settings.
// Returns nil for vanilla K8s (no OpenShift APIServer resource exists).
// Returns error if on OpenShift but fails to read config (indicates a problem).
func getClusterTLSConfig(ctx context.Context, k8sClient client.Client) (*tls.Config, error) {
	logger := log.FromContext(ctx).WithName("tlsconfig")

	apiServer := &configv1.APIServer{}
	err := k8sClient.Get(ctx, client.ObjectKey{Name: "cluster"}, apiServer)

	if err != nil {
		if meta.IsNoMatchError(err) {
			// Vanilla K8s - config.openshift.io API group not installed
			// Note: IsNotFound would indicate the API exists but object is missing (OpenShift misconfiguration)
			// Note: IsNotRegisteredError indicates operator bug (missing scheme registration)
			logger.Info("OpenShift APIServer config not found - running on vanilla K8s, using library defaults")
			return nil, nil
		}
		// On OpenShift but failed to read config - could be RBAC, missing object, or scheme registration issue
		return nil, fmt.Errorf("failed to get OpenShift APIServer config: %w", err)
	}

	// On OpenShift - use cluster TLS profile
	// Determine the effective profile type (defaults to Intermediate if not specified)
	profileType := configv1.TLSProfileIntermediateType
	if apiServer.Spec.TLSSecurityProfile != nil {
		profileType = apiServer.Spec.TLSSecurityProfile.Type
	}
	logger.Info("using OpenShift cluster TLS profile", "type", string(profileType))

	profile, err := getEffectiveTLSProfile(apiServer.Spec.TLSSecurityProfile, profileType)
	if err != nil {
		return nil, fmt.Errorf("failed to get effective TLS profile: %w", err)
	}

	return convertProfileToTLSConfig(ctx, profile)
}

// getEffectiveTLSProfile returns the effective TLS profile based on the APIServer configuration.
// Returns an error if the profile type is invalid or misconfigured.
func getEffectiveTLSProfile(profileSpec *configv1.TLSSecurityProfile, profileType configv1.TLSProfileType) (*configv1.TLSProfileSpec, error) {
	// Handle custom profile separately
	if profileType == configv1.TLSProfileCustomType {
		if profileSpec == nil || profileSpec.Custom == nil {
			return nil, fmt.Errorf("Custom TLS profile is invalid or incomplete")
		}
		spec := profileSpec.Custom.TLSProfileSpec
		return &spec, nil
	}

	// For Old, Intermediate, Modern - lookup in the predefined profiles map
	if profile, ok := configv1.TLSProfiles[profileType]; ok {
		return profile, nil
	}

	// Unknown profile type
	return nil, fmt.Errorf("unknown TLS profile type: %s", profileType)
}

// convertProfileToTLSConfig converts an OpenShift TLS profile to a tls.Config
// using library-go utilities for proper cipher suite conversion
func convertProfileToTLSConfig(ctx context.Context, profile *configv1.TLSProfileSpec) (*tls.Config, error) {
	logger := log.FromContext(ctx).WithName("tlsconfig")

	if profile == nil {
		return nil, fmt.Errorf("nil TLS profile")
	}

	// Use library-go to convert TLS version string to uint16
	minTLSVersion, err := crypto.TLSVersion(string(profile.MinTLSVersion))
	if err != nil {
		return nil, fmt.Errorf("invalid TLS version %s: %w", profile.MinTLSVersion, err)
	}

	tlsConfig := &tls.Config{
		MinVersion: minTLSVersion,
	}

	// Convert cipher suite names using library-go
	// OpenShift TLS profiles use OpenSSL names for TLS 1.2 ciphers (e.g., ECDHE-RSA-AES128-GCM-SHA256)
	// and IANA names for TLS 1.3 ciphers (e.g., TLS_AES_128_GCM_SHA256)
	// library-go's CipherSuite function handles the conversion to Go's uint16 constants
	if len(profile.Ciphers) > 0 {
		// Pre-allocate with capacity to avoid slice growth reallocations
		cipherSuites := make([]uint16, 0, len(profile.Ciphers))
		var invalidCiphers []string

		for _, cipherName := range profile.Ciphers {
			cipher, err := crypto.CipherSuite(cipherName)
			if err != nil {
				// OpenShift profiles use OpenSSL names for TLS 1.2 ciphers; translate to IANA and retry
				if iana := crypto.OpenSSLToIANACipherSuites([]string{cipherName}); len(iana) == 1 {
					cipher, err = crypto.CipherSuite(iana[0])
				}
			}
			if err != nil {
				// Log and skip invalid ciphers rather than failing
				// This matches OpenShift behavior of silently dropping invalid ciphers
				logger.Info("skipping invalid cipher suite in cluster TLS profile",
					"cipher", cipherName, "error", err.Error())
				invalidCiphers = append(invalidCiphers, cipherName)
				continue
			}
			cipherSuites = append(cipherSuites, cipher)
		}

		if len(cipherSuites) > 0 {
			tlsConfig.CipherSuites = cipherSuites
		} else if len(profile.Ciphers) > 0 {
			// All ciphers invalid - fail to match OpenShift admission validation behavior
			// OpenShift's admission webhook rejects configs with no valid ciphers (validate_apiserver.go)
			// Operators should fail rather than silently degrade when cluster TLS policy cannot be applied
			return nil, fmt.Errorf("all cipher suites in cluster TLS profile are invalid: %v", invalidCiphers)
		}
	}

	return tlsConfig, nil
}
