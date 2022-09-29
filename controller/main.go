package main

import (
	"context"
	"flag"
	"os"
	"time"

	"github.com/datawire/dlib/dgroup"
	"github.com/datawire/dlib/dlog"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"

	// "github.com/linkerd/linkerd-smi/pkg/adaptor"
	"github.com/linkerd/linkerd-smi/pkg/adaptor"
	spclientset "github.com/linkerd/linkerd2/controller/gen/client/clientset/versioned"
	k8sAPI "github.com/linkerd/linkerd2/controller/k8s"
	"github.com/linkerd/linkerd2/pkg/admin"
	"github.com/linkerd/linkerd2/pkg/k8s"
	hrclientset "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned"
	hrinformers "sigs.k8s.io/gateway-api/pkg/client/informers/externalversions/apis/v1alpha2"
)

func main() {
	ctx := context.Background()

	parser := flag.NewFlagSet("gamma", flag.ExitOnError)

	kubeConfigPath := parser.String("kubeconfig", "", "path to kube config")
	metricsAddr := parser.String("metrics-addr", ":9995", "address to serve scrapable metrics on")
	clusterDomain := parser.String("cluster-domain", "cluster.local", "kubernetes cluster domain")
	workers := parser.Int("worker-threads", 2, "number of concurrent goroutines to process the workqueue")

	grp := dgroup.NewGroup(ctx, dgroup.GroupConfig{
		EnableSignalHandling: true,
	})

	dlog.Infof(ctx, "Using cluster domain: %s", *clusterDomain)

	config, err := k8s.GetConfig(*kubeConfigPath, "")
	if err != nil {
		dlog.Errorf(ctx, "error configuring Kubernetes API client: %v", err)
		os.Exit(1)
	}

	// Set up for Kubernetes...
	k8sAPI, err := k8sAPI.InitializeAPI(
		ctx,
		*kubeConfigPath,
		false,
		k8sAPI.SP, k8sAPI.TS,
	)
	if err != nil {
		dlog.Errorf(ctx, "Failed to initialize K8s API: %s", err)
		os.Exit(1)
	}

	// Create ServiceProfile and TrafficSplit clientsets
	spClient, err := spclientset.NewForConfig(config)
	if err != nil {
		dlog.Errorf(ctx, "Error building serviceprofile clientset: %s", err.Error())
		os.Exit(1)
	}

	hrClient, err := hrclientset.NewForConfig(config)
	if err != nil {
		dlog.Errorf(ctx, "Error building HTTPRoute clientset: %s", err.Error())
		os.Exit(1)
	}

	// Watch for TrafficSplit changes.
	hrInformer := hrinformers.NewHTTPRouteInformer(hrClient, v1.NamespaceAll, 10*time.Minute, cache.Indexers{})

	// hrInformerFactory := hrinformers.NewSharedInformerFactory(hrClient, 10*time.Minute)

	controller := adaptor.NewController(
		k8sAPI.Client,
		*clusterDomain,
		hrClient,
		spClient,
		hrInformerFactory.Split().V1alpha1().TrafficSplits(),
		*workers,
	)

	grp.Go("admin-server", func(ctx context.Context) error {
		admin.StartServer(*metricsAddr)
		return nil
	})

	// Start the informer factory.
	grp.Go("ts-informer-factory", func(ctx context.Context) error {
		hrInformer.Run(ctx.Done())

		// hrInformerFactory.Start(ctx.Done())

		// // XXX This is disgusting. Basically the informer factory runs until ctx.Done()
		// // closes, so, yeah, we'll block here for that.
		// <-ctx.Done()
		// return nil
	})

	grp.Go("controller", func(ctx context.Context) error {
		// Run the controller until a shutdown signal is received
		if err = controller.Run(ctx.Done()); err != nil {
			dlog.Errorf(ctx, "Error running controller: %s", err.Error())
			return err
		}

		return nil
	})

	if err := grp.Wait(); err != nil {
		dlog.Errorf(ctx, "finished with error: %v", err)
		os.Exit(1)
	}
}
