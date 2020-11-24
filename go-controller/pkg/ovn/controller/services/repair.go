package services

import (
	"fmt"
	"strings"
	"time"

	"github.com/pkg/errors"

	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/metrics"
	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/ovn/loadbalancer"
	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/util"

	v1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1beta1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"

	corelisters "k8s.io/client-go/listers/core/v1"
	discoverylisters "k8s.io/client-go/listers/discovery/v1beta1"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	utilnet "k8s.io/utils/net"
)

// Repair is a controller loop that periodically examines all services in Kubernetes and
// reconciles them with the stored in OVN
//
// Based on:
// https://raw.githubusercontent.com/kubernetes/kubernetes/release-1.19/pkg/registry/core/service/ipallocator/controller/repair.go
//
// Can be run at infrequent intervals, and is best performed on startup of the master.
// Is level driven and idempotent - all valid service VIPs will be updated into the OVN db
// at the end of a single execution loop if no race is encountered.
type Repair struct {
	interval            time.Duration
	serviceLister       corelisters.ServiceLister
	endpointSliceLister discoverylisters.EndpointSliceLister
	recorder            record.EventRecorder
}

// NewRepair creates a controller that periodically ensures that all clusterIPs are uniquely allocated across the cluster
// and generates informational warnings for a cluster that is not in sync.
func NewRepair(interval time.Duration,
	serviceLister corelisters.ServiceLister,
	endpointSliceLister discoverylisters.EndpointSliceLister,
	recorder record.EventRecorder,
) *Repair {
	return &Repair{
		interval:            interval,
		serviceLister:       serviceLister,
		endpointSliceLister: endpointSliceLister,
		recorder:            recorder,
	}
}

// RunUntil starts the controller until the provided ch is closed.
func (c *Repair) RunUntil(stopCh <-chan struct{}) {
	wait.Until(func() {
		if err := c.RunOnce(); err != nil {
			klog.Errorf("Error during full-sync of services: %v")
		}
	}, c.interval, stopCh)
}

// RunOnce verifies the state of Services and OVN LBs and returns an error if an unrecoverable problem occurs.
func (c *Repair) RunOnce() error {
	return retry.RetryOnConflict(retry.DefaultBackoff, c.runOnce)
}

// serviceLoadBalancer contains a representation of a Service Loadbalancer
// with the vip as key and []string as Endpoints, both in the IP:Port format
type serviceLoadBalancer map[string][]string

// runOnce verifies the state of the cluster OVN LB VIP allocations and returns an error if an unrecoverable problem occurs.
func (c *Repair) runOnce() error {
	startTime := time.Now()
	klog.V(4).Infof("Starting full-sync of services")
	defer func() {
		klog.V(4).Infof("Finished doing full-sync of services: %v", time.Since(startTime))
		metrics.MetricSyncServiceLatency.WithLabelValues("full-sync").Observe(time.Since(startTime).Seconds())
	}()
	// We populate the struct first so we can compare and reconcile only the differences
	k8sClusterIPLb := make(map[v1.Protocol]serviceLoadBalancer)
	ovnClusterIPLb := make(map[v1.Protocol]serviceLoadBalancer)
	// cache the OVN LB
	ovnLBCache := make(map[v1.Protocol]string)
	// Initialize the nested maps and the LB cache
	protocols := []v1.Protocol{v1.ProtocolSCTP, v1.ProtocolTCP, v1.ProtocolUDP}
	for _, p := range protocols {
		k8sClusterIPLb[p] = make(map[string][]string)
		ovnClusterIPLb[p] = make(map[string][]string)
		lbUUID, err := loadbalancer.GetOVNKubeLoadBalancer(p)
		if err != nil {
			return errors.Wrapf(err, "Failed to get OVN load balancer for protocol %s", p)
		}
		ovnLBCache[p] = lbUUID
	}

	// List all services in the indexer
	list, err := c.serviceLister.List(labels.Everything())
	if err != nil {
		return fmt.Errorf("unable to refresh the service VIP list: %v", err)
	}

	// Get Kubernetes State
	for _, service := range list {
		// doesn't need OVN Load Balancer
		if !util.ServiceTypeHasClusterIP(service) || !util.IsClusterIPSet(service) {
			continue
		}
		// Get endpoints belonging to the Service
		esLabelSelector := labels.Set(map[string]string{
			discovery.LabelServiceName: service.Name,
		}).AsSelectorPreValidated()
		endpointSlices, err := c.endpointSliceLister.EndpointSlices(service.Namespace).List(esLabelSelector)
		if err != nil {
			return err
		}
		// Obtain VIPs:Endpoints
		for _, ip := range service.Spec.ClusterIPs {
			for _, svcPort := range service.Spec.Ports {
				vip := util.JoinHostPortInt32(ip, svcPort.Port)
				// get the endpoints associated to the vip
				family := v1.IPv4Protocol
				if utilnet.IsIPv6String(ip) {
					family = v1.IPv6Protocol
				}
				eps := getLbEndpoints(endpointSlices, svcPort.Protocol, family)
				k8sClusterIPLb[svcPort.Protocol][vip] = eps
				// Insert a reject ACL if the service doesn't have endpoints associated
				if len(eps) == 0 {
					klog.V(4).Infof("Service %s/%s without endpoints", service.Name, service.Namespace)
				}
			}
		}
	}

	// Get OVN State
	for _, p := range protocols {
		// we need to process the output {"192.168.0.1:80"="10.1.1.1:80,10.2.2.2:80"}
		// to get an slice with the endpoints
		loadBalancerVIPs, err := loadbalancer.GetLoadBalancerVIPs(ovnLBCache[p])
		if err != nil {
			return errors.Wrapf(err, "Failed to get load balancer vips for %s", ovnLBCache[p])
		}
		for vip, eps := range loadBalancerVIPs {
			ovnClusterIPLb[p][vip] = strings.Split(eps, ",")
		}
	}

	// Reconcile
	for _, p := range protocols {
		if apiequality.Semantic.DeepEqual(k8sClusterIPLb[p], ovnClusterIPLb[p]) {
			continue
		}
		// Update OVN with the missing Service VIP
		for vip, eps := range k8sClusterIPLb[p] {
			ovnEndpoints, ok := ovnClusterIPLb[p][vip]
			// check if the Service vip exists in OVN and has the same endpoints
			// We always update the whole service with all the endpoints
			// TODO: check if updating only the missing endpoints is better
			if !ok || !apiequality.Semantic.DeepEqual(eps, ovnEndpoints) {
				if err := loadbalancer.UpdateLoadBalancer(ovnLBCache[p], vip, eps); err != nil {
					return errors.Wrapf(err, "error trying to update load balancer %s with VIP %s and endpoints %v", ovnLBCache[p], vip, eps)
				}
			}
		}
		// Delete stale data in OVN
		for vip := range ovnClusterIPLb[p] {
			if _, ok := k8sClusterIPLb[p][vip]; !ok {
				if err := loadbalancer.DeleteLoadBalancerVIP(ovnLBCache[p], vip); err != nil {
					return errors.Wrapf(err, "Failed to delete load balancer vips %s for %s", vip, ovnLBCache[p])
				}
			}
		}
	}
	return nil
}
