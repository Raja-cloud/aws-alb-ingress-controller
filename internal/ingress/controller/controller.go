package controller

import (
	"fmt"

	"github.com/kubernetes-sigs/aws-alb-ingress-controller/internal/alb/generator"
	"github.com/kubernetes-sigs/aws-alb-ingress-controller/internal/alb/lb"
	"github.com/kubernetes-sigs/aws-alb-ingress-controller/internal/alb/ls"
	"github.com/kubernetes-sigs/aws-alb-ingress-controller/internal/alb/rs"
	"github.com/kubernetes-sigs/aws-alb-ingress-controller/internal/alb/sg"
	"github.com/kubernetes-sigs/aws-alb-ingress-controller/internal/alb/tags"
	"github.com/kubernetes-sigs/aws-alb-ingress-controller/internal/alb/tg"
	"github.com/kubernetes-sigs/aws-alb-ingress-controller/internal/aws"
	"github.com/kubernetes-sigs/aws-alb-ingress-controller/internal/ingress/backend"
	"github.com/kubernetes-sigs/aws-alb-ingress-controller/internal/ingress/controller/config"
	"github.com/kubernetes-sigs/aws-alb-ingress-controller/internal/ingress/controller/handlers"
	"github.com/kubernetes-sigs/aws-alb-ingress-controller/internal/ingress/controller/store"
	"github.com/kubernetes-sigs/aws-alb-ingress-controller/internal/ingress/metric"
	corev1 "k8s.io/api/core/v1"
	extensions "k8s.io/api/extensions/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

func Initialize(config *config.Configuration, mgr manager.Manager, mc metric.Collector, cloud aws.CloudAPI) error {
	reconciler, err := newReconciler(config, mgr, mc, cloud)
	if err != nil {
		return err
	}
	c, err := controller.New("alb-ingress-controller", mgr, controller.Options{Reconciler: reconciler})
	if err != nil {
		return err
	}
	if err := config.BindDynamicSettings(mgr, c, cloud); err != nil {
		return err
	}

	if err := watchClusterEvents(c, mgr.GetCache(), config.IngressClass); err != nil {
		return fmt.Errorf("failed to watch cluster events due to %v", err)
	}

	return nil
}

func newReconciler(config *config.Configuration, mgr manager.Manager, mc metric.Collector, cloud aws.CloudAPI) (reconcile.Reconciler, error) {
	store, err := store.New(mgr, config)
	if err != nil {
		return nil, err
	}
	nameTagGenerator := generator.NewNameTagGenerator(*config)
	tagsController := tags.NewController(cloud)
	endpointResolver := backend.NewEndpointResolver(store, cloud)
	tgGroupController := tg.NewGroupController(cloud, store, nameTagGenerator, tagsController, endpointResolver)
	rsController := rs.NewController(cloud)
	lsGroupController := ls.NewGroupController(store, cloud, rsController)
	sgAssociationController := sg.NewAssociationController(store, cloud, tagsController, nameTagGenerator)
	lbController := lb.NewController(cloud, store,
		nameTagGenerator, tgGroupController, lsGroupController, sgAssociationController, tagsController)

	return &Reconciler{
		client:          mgr.GetClient(),
		cache:           mgr.GetCache(),
		recorder:        mgr.GetRecorder("alb-ingress-controller"),
		store:           store,
		lbController:    lbController,
		metricCollector: mc,
	}, nil
}

func watchClusterEvents(c controller.Controller, cache cache.Cache, ingressClass string) error {
	if err := c.Watch(&source.Kind{Type: &extensions.Ingress{}}, &handlers.EnqueueRequestsForIngressEvent{
		IngressClass: ingressClass,
	}); err != nil {
		return err
	}
	if err := c.Watch(&source.Kind{Type: &corev1.Service{}}, &handlers.EnqueueRequestsForServiceEvent{
		IngressClass: ingressClass,
		Cache:        cache,
	}); err != nil {
		return err
	}
	if err := c.Watch(&source.Kind{Type: &corev1.Endpoints{}}, &handlers.EnqueueRequestsForEndpointsEvent{
		IngressClass: ingressClass,
		Cache:        cache,
	}); err != nil {
		return err
	}
	if err := c.Watch(&source.Kind{Type: &corev1.Node{}}, &handlers.EnqueueRequestsForNodeEvent{
		IngressClass: ingressClass,
		Cache:        cache,
	}); err != nil {
		return err
	}
	return nil
}
