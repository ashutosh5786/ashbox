// Command ashbox-controller runs the sandbox controller in a cluster.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"

	v1alpha1 "github.com/ashutosh5786/ashbox/api/v1alpha1"
	"github.com/ashutosh5786/ashbox/internal/controller"
	"github.com/ashutosh5786/ashbox/internal/status"
)

type options struct {
	metricsAddr    string
	probeAddr      string
	leaderElect    bool
	baggageKey     string
	mesh           controller.Mesh
	degradedAfter  time.Duration
	recoveredAfter time.Duration
	zap            zap.Options
}

func parseFlags(args []string) (options, error) {
	var o options
	var mesh string
	fs := flag.NewFlagSet("ashbox-controller", flag.ContinueOnError)
	fs.StringVar(&o.metricsAddr, "metrics-bind-address", ":8080", "address of the metrics endpoint; 0 disables it")
	fs.StringVar(&o.probeAddr, "health-probe-bind-address", ":8081", "address of the health and readiness probes")
	fs.BoolVar(&o.leaderElect, "leader-elect", false, "elect a leader, so several replicas can run")
	fs.StringVar(&o.baggageKey, "baggage-key", "routing-key", "baggage entry that carries the routing key")
	fs.StringVar(&mesh, "mesh", string(controller.MeshNone), "mesh-specific checks: none or istio")
	fs.DurationVar(&o.degradedAfter, "degraded-after", status.DefaultSettings.DegradedAfter, "a fork counts as down after being not ready this long")
	fs.DurationVar(&o.recoveredAfter, "recovered-after", status.DefaultSettings.RecoveredAfter, "a down fork counts as up after being ready this long")
	o.zap.BindFlags(fs)
	if err := fs.Parse(args); err != nil {
		return o, err
	}

	switch controller.Mesh(mesh) {
	case controller.MeshNone, controller.MeshIstio:
		o.mesh = controller.Mesh(mesh)
	default:
		return o, fmt.Errorf("unknown mesh %q (want none or istio)", mesh)
	}
	if o.baggageKey == "" {
		return o, fmt.Errorf("baggage-key must not be empty")
	}
	if o.degradedAfter <= 0 || o.recoveredAfter <= 0 {
		return o, fmt.Errorf("degraded-after and recovered-after must be positive")
	}
	return o, nil
}

func newScheme() (*runtime.Scheme, error) {
	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		clientgoscheme.AddToScheme,
		gatewayv1.Install,
		gatewayv1beta1.Install,
		v1alpha1.AddToScheme,
	} {
		if err := add(s); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ashbox-controller:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	o, err := parseFlags(args)
	if err != nil {
		return err
	}
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&o.zap)))

	scheme, err := newScheme()
	if err != nil {
		return err
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: o.metricsAddr},
		HealthProbeBindAddress: o.probeAddr,
		LeaderElection:         o.leaderElect,
		LeaderElectionID:       "ashbox-controller.ashbox.dev",
	})
	if err != nil {
		return fmt.Errorf("manager: %w", err)
	}

	r := &controller.SandboxReconciler{
		Client:     mgr.GetClient(),
		BaggageKey: o.baggageKey,
		Mesh:       o.mesh,
		Phases:     status.Settings{DegradedAfter: o.degradedAfter, RecoveredAfter: o.recoveredAfter},
	}
	if err := r.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("controller: %w", err)
	}
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return err
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return err
	}
	return mgr.Start(ctrl.SetupSignalHandler())
}
