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
	"bufio"
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"

	"go.uber.org/zap/zapcore"

	pkgruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/medik8s/fence-agents-remediation/api/v1alpha1"
	"github.com/medik8s/fence-agents-remediation/controllers"

	//+kubebuilder:scaffold:imports
	"github.com/medik8s/fence-agents-remediation/pkg/cli"
	"github.com/medik8s/fence-agents-remediation/version"
)

const (
	AGENTS_FILE = "fence_agents_list"
)

var (
	scheme   = pkgruntime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	opts := zap.Options{
		Development: true,
		TimeEncoder: zapcore.RFC3339NanoTimeEncoder,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	printVersion()
	agentList, err := getFenceAgents(AGENTS_FILE)
	if err != nil {
		setupLog.Error(err, "unable to check whether the fence agent file exists")
		os.Exit(1)
	}
	printSupportedFenceAgents(agentList)

	// Disable HTTP/2 support to avoid issues with CVE HTTP/2 Rapid Reset.
	// Currently, the metrics server enables/disables HTTP/2 support only if SecureServing is enabled, which is not.
	// Adding the disabling logic anyway to avoid future issues.
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling HTTP/2 support")
		c.NextProtos = []string{"http/1.1"}
	}

	metricsOpts := server.Options{
		BindAddress: metricsAddr,
		TLSOpts:     []func(*tls.Config){disableHTTP2},
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsOpts,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "cb305759.medik8s.io",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	executer, err := cli.NewExecuter(mgr.GetClient())
	if err != nil {
		setupLog.Error(err, "unable to create executer")
		os.Exit(1)
	}

	if err = (&controllers.FenceAgentsRemediationReconciler{
		Client:     mgr.GetClient(),
		Log:        ctrl.Log.WithName("controllers").WithName("FenceAgentsRemediation"),
		Scheme:     mgr.GetScheme(),
		Executor:   executer,
		AgentsList: agentList,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "FenceAgentsRemediation")
		os.Exit(1)
	}

	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
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

func printSupportedFenceAgents(agentList []string) {
	setupLog.Info(fmt.Sprintf("Fence agents: %d", len(agentList)))
	setupLog.Info(fmt.Sprintf("Available agents: %s", strings.Join(agentList, " ")))
}

// getFenceAgents check if the file exists, read it, and then remove redundant prefix
func getFenceAgents(filePath string) ([]string, error) {
	if _, err := os.Stat(filePath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("fence agents file %s was not found with error %v", AGENTS_FILE, err)
		}
		return nil, fmt.Errorf("failed to check for supported fence agents list: %v", err)
	}

	fenceAgentFile, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer fenceAgentFile.Close()

	var lines []string
	scanner := bufio.NewScanner(fenceAgentFile)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error scanning the fenceAgentFile content: %v from file %s", err, AGENTS_FILE)
	}

	return lines, nil
}
