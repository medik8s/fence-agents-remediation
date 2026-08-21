/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"go.uber.org/zap/zapcore"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	pkgruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	configv1 "github.com/openshift/api/config/v1"
	openshifttls "github.com/openshift/controller-runtime-common/pkg/tls"

	fenceagentsremediationv1alpha1 "github.com/medik8s/fence-agents-remediation/api/v1alpha1"
	"github.com/medik8s/fence-agents-remediation/internal/controller"

	//+kubebuilder:scaffold:imports
	webhookv1alpha1 "github.com/medik8s/fence-agents-remediation/internal/webhook/v1alpha1"
	"github.com/medik8s/fence-agents-remediation/pkg/cli"
	"github.com/medik8s/fence-agents-remediation/pkg/validation"
	"github.com/medik8s/fence-agents-remediation/version"
)

const (
	WebhookCertDir     = "/apiserver.local.config/certificates"
	WebhookCertName    = "apiserver.crt"
	WebhookKeyName     = "apiserver.key"
	farControllerName  = "FenceAgentsRemediation"
	fartControllerName = "FenceAgentsRemediationTemplate"
)

var (
	scheme   = pkgruntime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(configv1.Install(scheme))

	utilruntime.Must(fenceagentsremediationv1alpha1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

//+kubebuilder:rbac:groups=config.openshift.io,resources=apiservers,verbs=get;list;watch

func main() {
	var (
		metricsAddr          string
		enableLeaderElection bool
		probeAddr            string
		enableHTTP2          bool
		webhookOpts          webhook.Options
	)
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8443", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false, "If HTTP/2 should be enabled for the metrics and webhook servers.")

	opts := zap.Options{
		Development: true,
		TimeEncoder: zapcore.RFC3339NanoTimeEncoder,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	printVersion()

	configureWebhookOpts(&webhookOpts, enableHTTP2)

	// TLS options for metrics server: disable HTTP/2 for mitigating CVEs
	var tlsOpts []func(*tls.Config)
	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, func(c *tls.Config) {
			c.NextProtos = []string{"http/1.1"}
		})
	}

	// Fetch OpenShift TLS profile and apply to webhook and metrics servers
	cfg := ctrl.GetConfigOrDie()
	setupClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		setupLog.Error(err, "unable to create setup client")
		os.Exit(1)
	}

	isOpenShift := true
	fetchCtx, fetchCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer fetchCancel()
	tlsProfile, err := openshifttls.FetchAPIServerTLSProfile(fetchCtx, setupClient)
	if err != nil {
		if meta.IsNoMatchError(err) || apierrors.IsNotFound(err) {
			setupLog.Info("Not on OpenShift, using default TLS settings")
			isOpenShift = false
		} else {
			setupLog.Error(err, "failed to fetch TLS profile")
			os.Exit(1)
		}
	}

	if isOpenShift {
		tlsConfig, unsupported := openshifttls.NewTLSConfigFromProfile(tlsProfile)
		if len(unsupported) > 0 {
			setupLog.Info("Unsupported TLS ciphers ignored", "ciphers", unsupported)
		}
		tlsOpts = append(tlsOpts, tlsConfig)
		webhookOpts.TLSOpts = append(webhookOpts.TLSOpts, tlsConfig)
	}

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
		Metrics: server.Options{
			BindAddress:    metricsAddr,
			SecureServing:  true,
			FilterProvider: filters.WithAuthenticationAndAuthorization,
			TLSOpts:        tlsOpts,
		},
		WebhookServer:          webhook.NewServer(webhookOpts),
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "cb305759.medik8s.io",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	outOfServiceTaintValidator, err := validation.NewOutOfServiceTaintValidator(mgr.GetConfig())
	if err != nil {
		setupLog.Error(err, "unable to verify Kubernetes version for indicating the out-of-service taint support. out-of-service taint isn't supported")
	}
	isOutOfServiceTaintSupported := outOfServiceTaintValidator.IsOutOfServiceTaintSupported()
	if isOutOfServiceTaintSupported {
		setupLog.Info("out-of-service taint is supported on this cluster")
	}
	fenceagentsremediationv1alpha1.SetOutOfServiceTaintSupported(isOutOfServiceTaintSupported)

	executer, err := cli.NewExecuter(mgr.GetClient(), mgr.GetEventRecorderFor(farControllerName+"-executer"))
	if err != nil {
		setupLog.Error(err, "unable to create executer")
		os.Exit(1)
	}

	if err = (&controller.FenceAgentsRemediationReconciler{
		Client:   mgr.GetClient(),
		Log:      ctrl.Log.WithName("controller").WithName(farControllerName),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor(farControllerName),
		Executor: executer,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", farControllerName)
		os.Exit(1)
	}

	if err = webhookv1alpha1.SetupFARWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "FenceAgentsRemediation")
		os.Exit(1)
	}
	if err = webhookv1alpha1.SetupFARTemplateWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "FenceAgentsRemediationTemplate")
		os.Exit(1)
	}
	if err = (&controller.FenceAgentsRemediationTemplateReconciler{
		Client:   mgr.GetClient(),
		Log:      ctrl.Log.WithName("controller").WithName(fartControllerName),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor(fartControllerName),
		Executor: executer,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "FenceAgentsRemediationTemplate")
		os.Exit(1)
	}
	//+kubebuilder:scaffold:builder

	// Set up TLS profile watcher for dynamic profile changes (OpenShift only)
	ctx, cancel := context.WithCancel(ctrl.SetupSignalHandler())
	defer cancel()

	if isOpenShift {
		watcher := &openshifttls.SecurityProfileWatcher{
			Client:                mgr.GetClient(),
			InitialTLSProfileSpec: tlsProfile,
			OnProfileChange: func(_ context.Context, _, _ configv1.TLSProfileSpec) {
				setupLog.Info("TLS profile changed, restarting")
				cancel()
			},
		}
		if err := watcher.SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to set up TLS profile watcher")
			os.Exit(1)
		}
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func printVersion() {
	setupLog.Info(fmt.Sprintf("Go Version: %s", runtime.Version()))
	setupLog.Info(fmt.Sprintf("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH))
	setupLog.Info(fmt.Sprintf("Operator Version: %s", version.Version))
	setupLog.Info(fmt.Sprintf("Git Commit: %s", version.GitCommit))
	setupLog.Info(fmt.Sprintf("Build Date: %s", version.BuildDate))
}

func configureWebhookOpts(webhookOpts *webhook.Options, enableHTTP2 bool) {

	certs := []string{filepath.Join(WebhookCertDir, WebhookCertName), filepath.Join(WebhookCertDir, WebhookKeyName)}
	certsInjected := true
	for _, fname := range certs {
		if _, err := os.Stat(fname); err != nil {
			certsInjected = false
			break
		}
	}
	if certsInjected {
		webhookOpts.CertDir = WebhookCertDir
		webhookOpts.CertName = WebhookCertName
		webhookOpts.KeyName = WebhookKeyName
		webhookOpts.TLSOpts = []func(*tls.Config){}
	} else {
		setupLog.Info("OLM injected certs for webhooks not found")
	}
	// disable http/2 for mitigating relevant CVEs
	if !enableHTTP2 {
		webhookOpts.TLSOpts = append(webhookOpts.TLSOpts,
			func(c *tls.Config) {
				c.NextProtos = []string{"http/1.1"}
			},
		)
		setupLog.Info("HTTP/2 for webhooks disabled")
	} else {
		setupLog.Info("HTTP/2 for webhooks enabled")
	}

}
