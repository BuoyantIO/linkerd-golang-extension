package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/datawire/dlib/dlog"
	serviceprofile "github.com/linkerd/linkerd2/controller/gen/apis/serviceprofile/v1alpha2"
	spclientset "github.com/linkerd/linkerd2/controller/gen/client/clientset/versioned"

	// HTTPRoute "github.com/servicemeshinterface/smi-sdk-go/pkg/apis/split/v1alpha1"
	gatewayapi "sigs.k8s.io/gateway-api/apis/v1alpha2"
	hrclientset "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned"
	hrscheme "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned/scheme"
	hrinformers "sigs.k8s.io/gateway-api/pkg/client/informers/externalversions/apis/v1alpha2"

	// hrclientset "github.com/servicemeshinterface/smi-sdk-go/pkg/gen/client/split/clientset/versioned"
	// tsscheme "github.com/servicemeshinterface/smi-sdk-go/pkg/gen/client/split/clientset/versioned/scheme"
	// informers "github.com/servicemeshinterface/smi-sdk-go/pkg/gen/client/split/informers/externalversions/split/v1alpha1"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
)

const (

	// ignoreServiceProfileAnnotation is used with Service Profiles
	// to prevent the SMI adaptor from changing it
	ignoreServiceProfileAnnotation = "gamma.linkerd.io/skip"

	component = "gamma-controller"
)

type (
	// queueKey is the type used with the workqueue
	queueKey struct {
		name      string
		namespace string
		service   string
	}
)

// GAMMAController is an adaptor that converts information from Gateway API
// HTTPRoute resources into Linkerd ServiceProfile resources
type GAMMAController struct {
	kubeclientset kubernetes.Interface
	clusterDomain string

	// HTTPRoute clientset
	hrclientset hrclientset.Interface
	hrSynced    cache.InformerSynced

	// ServiceProfile clientset
	spclientset spclientset.Interface

	// workqueue is a rate limited work queue. This is used to queue work to be
	// processed instead of performing it as soon as a change happens. This
	// means we can ensure we only process a fixed amount of resources at a
	// time, and makes it easy to ensure we are never processing the same item
	// simultaneously in two different workers.
	workqueue workqueue.RateLimitingInterface

	// workers is the number of concurrent goroutines
	// to process items from the workqueue
	workers int

	// hrEventRecorder is used to generate events on the HTTPRoutes
	hrEventRecorder record.EventRecorder
}

// NewController returns a new GAMMA controller
func NewController(
	ctx context.Context,
	kubeclientset kubernetes.Interface,
	clusterDomain string,
	hrclientset hrclientset.Interface,
	spclientset spclientset.Interface,
	hrInformer hrinformers.HTTPRouteInformer,
	numberOfWorkers int) *GAMMAController {

	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{
		Interface: kubeclientset.CoreV1().Events(""),
	})

	controller := &GAMMAController{
		kubeclientset:   kubeclientset,
		clusterDomain:   clusterDomain,
		hrclientset:     hrclientset,
		hrSynced:        hrInformer.Informer().HasSynced,
		spclientset:     spclientset,
		workqueue:       workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "HTTPRoutes"),
		workers:         numberOfWorkers,
		hrEventRecorder: eventBroadcaster.NewRecorder(hrscheme.Scheme, corev1.EventSource{Component: component}),
	}

	// Set up an event handler for when HTTPRoute resources change
	hrInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			hr, ok := obj.(*gatewayapi.HTTPRoute)
			if !ok {
				log.Errorf("couldn't convert the object to a HTTPRoute")
				return
			}
			controller.enqueueHTTPRoute(ctx, *hr)
		},
		UpdateFunc: func(old, new interface{}) {
			hr, ok := new.(*gatewayapi.HTTPRoute)
			if !ok {
				log.Errorf("couldn't convert the object to a HTTPRoute")
				return
			}
			controller.enqueueHTTPRoute(ctx, *hr)
		},
		DeleteFunc: func(obj interface{}) {
			hr, ok := obj.(*gatewayapi.HTTPRoute)
			if !ok {
				log.Errorf("couldn't convert the object to a HTTPRoute")
				return
			}
			controller.enqueueHTTPRoute(ctx, *hr)
		},
	})

	return controller
}

// Run will set up the event handlers for types we are interested in, as well
// as syncing informer caches and starting workers. It will block until stopCh
// is closed, at which point it will shutdown the workqueue and wait for
// workers to finish processing their current work items.
func (c *GAMMAController) Run(ctx context.Context) error {
	defer c.workqueue.ShutDown()

	log.Info("Starting GAMMA Controller")

	// Wait for the caches to be synced before starting workers
	log.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(ctx.Done(), c.hrSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	log.Info("Starting workers")
	// Launch workers to process TS resources

	for i := 0; i < c.workers; i++ {
		go wait.Until(func() { c.runWorker(ctx) }, time.Second, ctx.Done())
	}

	log.Info("Started workers")
	<-ctx.Done()
	log.Info("Shutting down workers")

	return nil
}

// runWorker is a long-running function that will continually call the
// processNextWorkItem function in order to read and process a message on the
// workqueue.
func (c *GAMMAController) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem will read a single work item off the workqueue and
// attempt to process it, by calling the updateServiceProfile.
func (c *GAMMAController) processNextWorkItem(ctx context.Context) bool {
	obj, shutdown := c.workqueue.Get()

	if shutdown {
		return false
	}

	// XXX We can probably just inline this.
	err := c.processQueueKey(ctx, obj)

	if err != nil {
		log.Error(err)
		return true
	}

	return true
}

// processQueueKey is the main logic of the controller.
//
// XXX We can probably just move this back into processNextWorkItem. This
// shouldn't need to be a separate function (or a closure, which is what it
// originally was).
func (c *GAMMAController) processQueueKey(ctx context.Context, obj interface{}) error {
	// We call Done here so the workqueue knows we have finished
	// processing this item. We also must remember to call Forget if we
	// do not want this work item being re-queued. For example, we do
	// not call Forget if a transient error occurs, instead the item is
	// put back on the workqueue and attempted again after a back-off
	// period.
	defer c.workqueue.Done(obj)
	var key queueKey
	var ok bool
	// We expect queueKey to come off the workqueue. We do this as
	// the delayed nature of the workqueue means the items in the informer
	// cache may actually be more up to date that when the item was
	// initially put onto the workqueue.
	if key, ok = obj.(queueKey); !ok {
		// As the item in the workqueue is actually invalid, we call
		// Forget here else we'd go into a loop of attempting to
		// process a work item that is invalid.
		c.workqueue.Forget(obj)
		dlog.Errorf(ctx, "expected queueKey in workqueue but got %#v", obj)
		return nil
	}
	// Update the ServiceProfile for the HTTPRoute that belongs to this
	// queueKey.
	err := c.updateServiceProfile(ctx, key)
	if err != nil {
		// Put the item back on the workqueue to handle any transient errors.
		c.workqueue.AddRateLimited(key)
		return fmt.Errorf("error syncing '%s/%s': %s, requeuing", key.namespace, key.name, err.Error())
	}
	// Finally, if no error occurs we Forget this item so it does not
	// get queued again until another change happens.
	c.workqueue.Forget(obj)
	dlog.Infof(ctx, "Successfully synced '%s/%s'", key.namespace, key.name)
	return nil
}

// updateServiceProfile compares the actual state with the desired, and attempts to
// converge the two. It then updates the Status block of the Ts resource
// with the current status of the resource.
func (c *GAMMAController) updateServiceProfile(ctx context.Context, hrKey queueKey) error {
	// Construct the FQDN of the backend service early, because it can't change,
	// and there's no sense calling toFQDN() multiple times.
	serviceFQDN := c.toFQDN(hrKey.service, hrKey.namespace)

	// Get the HTTPRoute resource with this namespace/name
	hr, err := c.hrclientset.GatewayV1alpha2().HTTPRoutes(hrKey.namespace).Get(ctx, hrKey.name, metav1.GetOptions{})
	if err != nil {
		// Return if it's not a not found error
		if !errors.IsNotFound(err) {
			return err
		}

		// HTTPRoute does not exist anymore. Check if there is a relevant ServiceProfile
		// that we created, and if so, delete its dstOverrides.
		dlog.Infof(ctx, "HTTPRoute/%s is deleted, trying to clean up ServiceProfile %s", hrKey.name, serviceFQDN)

		sp, err := c.spclientset.LinkerdV1alpha2().ServiceProfiles(hrKey.namespace).Get(ctx, serviceFQDN, metav1.GetOptions{})
		if err != nil {
			// Return nil if not found, as no need to clean up
			if errors.IsNotFound(err) {
				return nil
			}
			return err
		}

		if ignoreAnnotationPresent(sp) {
			log.Infof("skipping clean up of serviceprofile/%s as ignore annotation is present", sp.Name)
			return nil
		}

		// Empty dstOverrides in the SP
		sp.Spec.DstOverrides = nil
		_, err = c.spclientset.LinkerdV1alpha2().ServiceProfiles(hrKey.namespace).Update(ctx, sp, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
		log.Infof("cleaned up `dstOverrides` of serviceprofile/%s", sp.Name)
		return nil
	}

	// Check if the Service Profile is already present
	sp, err := c.spclientset.LinkerdV1alpha2().ServiceProfiles(hr.Namespace).Get(ctx, serviceFQDN, metav1.GetOptions{})
	if err != nil {
		// Return if it's not a not found error
		if !errors.IsNotFound(err) {
			dlog.Errorf(ctx, "couldn't retrieve serviceprofile/%s: %s", serviceFQDN, err)
			return err
		}

		// Create a Service Profile resource as it does not exist
		sp, err = c.spclientset.LinkerdV1alpha2().ServiceProfiles(hr.Namespace).Create(ctx, c.toServiceProfile(hr, serviceFQDN), metav1.CreateOptions{})
		if err != nil {
			return err
		}
		c.hrEventRecorder.Eventf(hr, corev1.EventTypeNormal, "Created", "Created Service Profile %s", sp.Name)
		dlog.Infof(ctx, "created serviceprofile/%s for HTTPRoute/%s", sp.Name, hr.Name)
	} else {
		dlog.Infof(ctx, "serviceprofile/%s already present", sp.Name)
		if ignoreAnnotationPresent(sp) {
			dlog.Infof(ctx, "skipping update of serviceprofile/%s as ignore annotation is present", sp.Name)
			return nil
		}

		// Update the Service Profile
		updateDstOverrides(sp, hr, c.clusterDomain)
		_, err = c.spclientset.LinkerdV1alpha2().ServiceProfiles(hr.Namespace).Update(ctx, sp, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
		c.hrEventRecorder.Eventf(hr, corev1.EventTypeNormal, "Updated", "Updated Service Profile %s", sp.Name)
		log.Infof("updated serviceprofile/%s", sp.Name)
	}

	return nil
}

func ignoreAnnotationPresent(sp *serviceprofile.ServiceProfile) bool {
	_, ok := sp.Annotations[ignoreServiceProfileAnnotation]
	return ok
}

// makeServiceName: Utility function to take a Flagger HTTPRoute backendRef
// name and toss everything after the last "-" in the name.
func makeServiceName(name string) string {
	fields := strings.Split(name, "-")

	// If there is only one field, return it -- that means that there WAS no
	// "-" in the name.
	if len(fields) == 1 {
		return name
	}

	return strings.Join(fields[:len(fields)-1], "-")
}

// enqueueHTTPRoute takes a HTTPRoute resource and converts it into a key
// string which is then put onto the work queue. This method should *not* be
// passed resources of any type other than TS.
func (c *GAMMAController) enqueueHTTPRoute(ctx context.Context, hr gatewayapi.HTTPRoute) {
	if len(hr.Spec.ParentRefs) != 1 {
		dlog.Infof(ctx, "skipping HTTPRoute/%s as it does not have exactly one parentRef", hr.Name)
		return
	}

	pzero := hr.Spec.ParentRefs[0]

	if string(*pzero.Kind) != "Gateway" {
		dlog.Infof(ctx, "skipping HTTPRoute/%s as its parent isn't a Gateway", hr.Name)
		return
	}

	if strings.ToLower(string(pzero.Name)) != "linkerd" {
		dlog.Infof(ctx, "skipping HTTPRoute/%s as its Gateway isn't named linkerd", hr.Name)
		return
	}

	if len(hr.Spec.Rules) != 1 {
		dlog.Infof(ctx, "skipping HTTPRoute/%s as it does not have exactly one rule", hr.Name)
		return
	}

	if len(hr.Spec.Rules[0].BackendRefs) < 1 {
		dlog.Infof(ctx, "skipping HTTPRoute/%s as it does not any backendRefs", hr.Name)
		return
	}

	backendRefs := hr.Spec.Rules[0].BackendRefs
	backendService := ""

	for _, backend := range backendRefs {
		// Why the AF do I have to explicitly stringify backend.Name, when the
		// underlying type is a string? C'mon, Go.
		serviceName := makeServiceName(string(backend.Name))

		if backendService == "" {
			backendService = serviceName
		} else if backendService != serviceName {
			dlog.Infof(ctx, "skipping HTTPRoute/%s as it has multiple backendRefs", hr.Name)
			return
		}
	}

	c.workqueue.Add(
		queueKey{
			name:      hr.Name,
			namespace: hr.Namespace,
			service:   backendService,
		})
}

// updateDstOverrides updates the dstOverrides of the given serviceprofile
// to match that of the HTTPRoute
func updateDstOverrides(sp *serviceprofile.ServiceProfile, hr *gatewayapi.HTTPRoute, clusterDomain string) {
	backendRefs := hr.Spec.Rules[0].BackendRefs

	sp.Spec.DstOverrides = make([]*serviceprofile.WeightedDst, len(backendRefs))

	for i, backend := range backendRefs {
		namespace := hr.Namespace
		if backend.Namespace != nil {
			namespace = string(*backend.Namespace)
		}

		sp.Spec.DstOverrides[i] = &serviceprofile.WeightedDst{
			Authority: fqdn(string(backend.Name), namespace, clusterDomain),
			Weight:    *resource.NewQuantity(int64(*backend.Weight), resource.DecimalSI),
		}
	}
}

// toServiceProfile converts the given HTTPRoute into the relevant ServiceProfile resource
func (c *GAMMAController) toServiceProfile(hr *gatewayapi.HTTPRoute, serviceFQDN string) *serviceprofile.ServiceProfile {
	spResource := serviceprofile.ServiceProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceFQDN,
			Namespace: hr.Namespace,
		},
	}

	backendRefs := hr.Spec.Rules[0].BackendRefs

	for _, backend := range backendRefs {
		weightedDst := &serviceprofile.WeightedDst{
			Authority: serviceFQDN,
			Weight:    *resource.NewQuantity(int64(*backend.Weight), resource.DecimalSI),
		}

		spResource.Spec.DstOverrides = append(spResource.Spec.DstOverrides, weightedDst)
	}

	return &spResource
}

func (c *GAMMAController) toFQDN(service, namespace string) string {
	return fqdn(service, namespace, c.clusterDomain)
}

func fqdn(service, namespace, clusterDomain string) string {
	return fmt.Sprintf("%s.%s.svc.%s", service, namespace, clusterDomain)
}
