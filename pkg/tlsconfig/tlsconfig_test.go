package tlsconfig

import (
	"context"
	"crypto/tls"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/crypto"
)

func TestGetClusterTLSConfig(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = configv1.AddToScheme(scheme)

	tests := []struct {
		name         string
		apiServer    *configv1.APIServer
		expectMinTLS uint16
		expectError  bool
	}{
		{
			name: "No profile specified - defaults to Intermediate",
			apiServer: &configv1.APIServer{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Spec:       configv1.APIServerSpec{},
			},
			expectMinTLS: tls.VersionTLS12,
			expectError:  false,
		},
		{
			name: "Old profile",
			apiServer: &configv1.APIServer{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Spec: configv1.APIServerSpec{
					TLSSecurityProfile: &configv1.TLSSecurityProfile{
						Type: configv1.TLSProfileOldType,
					},
				},
			},
			expectMinTLS: tls.VersionTLS10,
			expectError:  false,
		},
		{
			name: "Intermediate profile",
			apiServer: &configv1.APIServer{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Spec: configv1.APIServerSpec{
					TLSSecurityProfile: &configv1.TLSSecurityProfile{
						Type: configv1.TLSProfileIntermediateType,
					},
				},
			},
			expectMinTLS: tls.VersionTLS12,
			expectError:  false,
		},
		{
			name: "Modern profile",
			apiServer: &configv1.APIServer{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Spec: configv1.APIServerSpec{
					TLSSecurityProfile: &configv1.TLSSecurityProfile{
						Type: configv1.TLSProfileModernType,
					},
				},
			},
			expectMinTLS: tls.VersionTLS13,
			expectError:  false,
		},
		{
			name: "Custom profile with TLS 1.3",
			apiServer: &configv1.APIServer{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Spec: configv1.APIServerSpec{
					TLSSecurityProfile: &configv1.TLSSecurityProfile{
						Type: configv1.TLSProfileCustomType,
						Custom: &configv1.CustomTLSProfile{
							TLSProfileSpec: configv1.TLSProfileSpec{
								MinTLSVersion: configv1.VersionTLS13,
								Ciphers: []string{
									"TLS_AES_128_GCM_SHA256",
									"TLS_AES_256_GCM_SHA384",
								},
							},
						},
					},
				},
			},
			expectMinTLS: tls.VersionTLS13,
			expectError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.apiServer).
				Build()

			ctx := context.Background()
			tlsConfig, err := getClusterTLSConfig(ctx, fakeClient)

			if (err != nil) != tt.expectError {
				t.Errorf("getClusterTLSConfig() error = %v, expectError %v", err, tt.expectError)
				return
			}

			if tlsConfig.MinVersion != tt.expectMinTLS {
				t.Errorf("MinVersion = %v, want %v", tlsConfig.MinVersion, tt.expectMinTLS)
			}
		})
	}
}

func TestGetClusterTLSConfig_VanillaK8s(t *testing.T) {
	// Test vanilla K8s behavior with proper scheme registration
	// On vanilla K8s, the configv1 types exist in the scheme but the CRD is not installed
	// This returns IsNoMatchError (no kind match) which we treat as vanilla K8s
	scheme := runtime.NewScheme()
	_ = configv1.AddToScheme(scheme)

	// Create a client without the APIServer object to simulate vanilla K8s
	// where the API group doesn't exist in the cluster
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	ctx := context.Background()
	_, err := getClusterTLSConfig(ctx, fakeClient)

	// With fake client, this returns IsNotFound (object not found) which now fails
	// This is actually correct - if the type is registered but object doesn't exist,
	// it means we're on OpenShift with a missing/deleted APIServer config
	// Real vanilla K8s would return IsNoMatchError (CRD not installed)

	// For test purposes, we expect an error since the fake client can't distinguish
	// between "CRD not installed" vs "object not found"
	if err == nil {
		t.Error("getClusterTLSConfig() expected error with fake client (can't simulate IsNoMatchError), got nil")
	}
}

func TestGetClusterTLSConfig_InvalidProfile(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = configv1.AddToScheme(scheme)

	tests := []struct {
		name      string
		apiServer *configv1.APIServer
		wantError string
	}{
		{
			name: "Custom profile type but Custom field is nil",
			apiServer: &configv1.APIServer{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Spec: configv1.APIServerSpec{
					TLSSecurityProfile: &configv1.TLSSecurityProfile{
						Type:   configv1.TLSProfileCustomType,
						Custom: nil,
					},
				},
			},
			wantError: "Custom TLS profile is invalid or incomplete",
		},
		{
			name: "Unknown profile type",
			apiServer: &configv1.APIServer{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Spec: configv1.APIServerSpec{
					TLSSecurityProfile: &configv1.TLSSecurityProfile{
						Type: configv1.TLSProfileType("UnknownType"),
					},
				},
			},
			wantError: "unknown TLS profile type: UnknownType",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.apiServer).
				Build()

			ctx := context.Background()
			_, err := getClusterTLSConfig(ctx, fakeClient)

			if err == nil {
				t.Errorf("getClusterTLSConfig() expected error containing %q, got nil", tt.wantError)
				return
			}

			if !strings.Contains(err.Error(), tt.wantError) {
				t.Errorf("getClusterTLSConfig() error = %q, want error containing %q", err.Error(), tt.wantError)
			}
		})
	}
}

func TestTLSVersionConversion(t *testing.T) {
	tests := []struct {
		name     string
		version  configv1.TLSProtocolVersion
		expected uint16
	}{
		{"TLS 1.0", configv1.VersionTLS10, tls.VersionTLS10},
		{"TLS 1.1", configv1.VersionTLS11, tls.VersionTLS11},
		{"TLS 1.2", configv1.VersionTLS12, tls.VersionTLS12},
		{"TLS 1.3", configv1.VersionTLS13, tls.VersionTLS13},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := crypto.TLSVersion(string(tt.version))
			if err != nil {
				t.Errorf("crypto.TLSVersion(%v) error = %v", tt.version, err)
			}
			if result != tt.expected {
				t.Errorf("crypto.TLSVersion(%v) = %v, want %v", tt.version, result, tt.expected)
			}
		})
	}
}

func TestCipherSuiteConversion(t *testing.T) {
	tests := []struct {
		name     string
		ciphers  []string
		expected int
	}{
		{
			name: "Valid TLS 1.3 ciphers",
			ciphers: []string{
				"TLS_AES_128_GCM_SHA256",
				"TLS_AES_256_GCM_SHA384",
				"TLS_CHACHA20_POLY1305_SHA256",
			},
			expected: 3,
		},
		{
			name: "Valid TLS 1.2 ciphers",
			ciphers: []string{
				"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
				"TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384",
			},
			expected: 2,
		},
		{
			name:     "Unknown ciphers ignored",
			ciphers:  []string{"UNKNOWN_CIPHER", "INVALID_CIPHER"},
			expected: 0,
		},
		{
			name: "Mix of valid and invalid",
			ciphers: []string{
				"TLS_AES_128_GCM_SHA256",
				"UNKNOWN_CIPHER",
				"TLS_AES_256_GCM_SHA384",
			},
			expected: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result []uint16
			for _, cipherName := range tt.ciphers {
				cipher, err := crypto.CipherSuite(cipherName)
				if err == nil {
					result = append(result, cipher)
				}
			}
			if len(result) != tt.expected {
				t.Errorf("crypto.CipherSuite() returned %d ciphers, want %d", len(result), tt.expected)
			}
		})
	}
}

func TestCreateTLSOptsForServer(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = configv1.AddToScheme(scheme)

	apiServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec: configv1.APIServerSpec{
			TLSSecurityProfile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileModernType,
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(apiServer).
		Build()

	ctx := context.Background()

	t.Run("With HTTP/2 disabled", func(t *testing.T) {
		tlsOpts, err := CreateTLSOptsForServer(ctx, fakeClient, true)
		if err != nil {
			t.Fatalf("CreateTLSOptsForServer() error = %v", err)
		}

		if len(tlsOpts) == 0 {
			t.Fatal("Expected TLS options, got none")
		}

		testConfig := &tls.Config{}
		for _, opt := range tlsOpts {
			opt(testConfig)
		}

		if testConfig.MinVersion != tls.VersionTLS13 {
			t.Errorf("MinVersion = %v, want TLS 1.3", testConfig.MinVersion)
		}

		if len(testConfig.NextProtos) != 1 || testConfig.NextProtos[0] != "http/1.1" {
			t.Errorf("NextProtos = %v, want [http/1.1]", testConfig.NextProtos)
		}
	})

	t.Run("With HTTP/2 enabled", func(t *testing.T) {
		tlsOpts, err := CreateTLSOptsForServer(ctx, fakeClient, false)
		if err != nil {
			t.Fatalf("CreateTLSOptsForServer() error = %v", err)
		}

		testConfig := &tls.Config{}
		for _, opt := range tlsOpts {
			opt(testConfig)
		}

		if testConfig.MinVersion != tls.VersionTLS13 {
			t.Errorf("MinVersion = %v, want TLS 1.3", testConfig.MinVersion)
		}

		if len(testConfig.NextProtos) > 0 {
			t.Errorf("NextProtos should be empty when HTTP/2 enabled, got %v", testConfig.NextProtos)
		}
	})
}

// TestApplyTLSConfigWithHTTP2Control removed - this functionality is now handled
// directly in CreateTLSOptsForServer which is tested separately

func TestConvertProfileToTLSConfig(t *testing.T) {
	tests := []struct {
		name          string
		profile       *configv1.TLSProfileSpec
		expectError   bool
		expectMinTLS  uint16
		expectCiphers int
	}{
		{
			name: "Valid profile with mixed cipher name formats",
			profile: &configv1.TLSProfileSpec{
				MinTLSVersion: configv1.VersionTLS12,
				Ciphers: []string{
					"ECDHE-RSA-AES128-GCM-SHA256",           // OpenSSL name
					"TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384", // IANA name
				},
			},
			expectError:   false,
			expectMinTLS:  tls.VersionTLS12,
			expectCiphers: 2,
		},
		{
			name: "Profile with some invalid ciphers should skip them",
			profile: &configv1.TLSProfileSpec{
				MinTLSVersion: configv1.VersionTLS12,
				Ciphers: []string{
					"TLS_AES_128_GCM_SHA256",
					"INVALID_CIPHER_NAME",
					"TLS_AES_256_GCM_SHA384",
				},
			},
			expectError:   false,
			expectMinTLS:  tls.VersionTLS12,
			expectCiphers: 2, // Only 2 valid ciphers
		},
		{
			name: "All invalid ciphers should fail",
			profile: &configv1.TLSProfileSpec{
				MinTLSVersion: configv1.VersionTLS12,
				Ciphers:       []string{"INVALID1", "INVALID2", "INVALID3"},
			},
			expectError: true,
		},
		{
			name: "Empty cipher list is valid",
			profile: &configv1.TLSProfileSpec{
				MinTLSVersion: configv1.VersionTLS13,
				Ciphers:       []string{},
			},
			expectError:   false,
			expectMinTLS:  tls.VersionTLS13,
			expectCiphers: 0,
		},
		{
			name: "TLS 1.3 profile",
			profile: &configv1.TLSProfileSpec{
				MinTLSVersion: configv1.VersionTLS13,
				Ciphers: []string{
					"TLS_AES_128_GCM_SHA256",
					"TLS_CHACHA20_POLY1305_SHA256",
				},
			},
			expectError:   false,
			expectMinTLS:  tls.VersionTLS13,
			expectCiphers: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			tlsConfig, err := convertProfileToTLSConfig(ctx, tt.profile)

			if (err != nil) != tt.expectError {
				t.Errorf("convertProfileToTLSConfig() error = %v, expectError %v", err, tt.expectError)
				return
			}

			if tt.expectError {
				return // Don't check further if we expected an error
			}

			if tlsConfig.MinVersion != tt.expectMinTLS {
				t.Errorf("MinVersion = %v, want %v", tlsConfig.MinVersion, tt.expectMinTLS)
			}

			if len(tlsConfig.CipherSuites) != tt.expectCiphers {
				t.Errorf("CipherSuites count = %v, want %v", len(tlsConfig.CipherSuites), tt.expectCiphers)
			}
		})
	}
}
