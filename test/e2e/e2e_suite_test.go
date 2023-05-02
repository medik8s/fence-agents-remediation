package e2e

import (
	"fmt"
	"testing"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"go.uber.org/zap/zapcore"

	"github.com/medik8s/fence-agents-remediation/api/v1alpha1"
	configclient "github.com/openshift/client-go/config/clientset/versioned"
	machineclient "github.com/openshift/machine-api-operator/pkg/generated/clientset/versioned/typed/machine/v1beta1"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.
var (
	log           logr.Logger
	clientSet     *kubernetes.Clientset
	k8sClient     ctrl.Client
	configClient  configclient.Interface
	machineClient *machineclient.MachineV1beta1Client
)

func TestE2e(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "FAR e2e suite")
}

var _ = BeforeSuite(func() {
	opts := zap.Options{
		Development: true,
		TimeEncoder: zapcore.RFC3339NanoTimeEncoder,
	}
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseFlagOptions(&opts)))
	log = logf.Log

	// +kubebuilder:scaffold:scheme

	// Load the Kubernetes configuration from the default location or from a specified kubeconfig file or simply die
	config, err := config.GetConfig()
	if err != nil {
		Fail(fmt.Sprintf("Couldn't get kubeconfig %v", err))
	}

	configClient, err = configclient.NewForConfig(config)
	Expect(err).NotTo(HaveOccurred())
	Expect(configClient).NotTo(BeNil())

	// Create a Kubernetes clientset using the configuration
	clientSet, err = kubernetes.NewForConfig(config)
	Expect(err).NotTo(HaveOccurred())
	Expect(clientSet).NotTo(BeNil())

	// Create a Machine clientset using the configuration
	machineClient, err = machineclient.NewForConfig(config)
	Expect(err).NotTo(HaveOccurred())
	Expect(machineClient).NotTo(BeNil())

	scheme.AddToScheme(scheme.Scheme)
	err = v1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err = ctrl.New(config, ctrl.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())

	debug()
})

func debug() {
	version, _ := clientSet.ServerVersion()
	fmt.Fprint(GinkgoWriter, version)
}
