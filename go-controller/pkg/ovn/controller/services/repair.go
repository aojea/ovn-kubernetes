package services

import (
	"time"

	"github.com/pkg/errors"

	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/metrics"
	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/ovn/gateway"
	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/ovn/loadbalancer"
	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/util"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"

	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
)

// Repair is a controller loop that can work as in one-shot or periodic mode
// it checks the OVN Load Balancers and delete the ones that doesn't exist in
// the Kubernetes Service Informer cache.
// Based on:
// https://raw.githubusercontent.com/kubernetes/kubernetes/release-1.19/pkg/registry/core/service/ipallocator/controller/repair.go
type Repair struct {
	interval time.Duration
	// serviceTracker tracks services and maps them to OVN LoadBalancers
	serviceLister corelisters.ServiceLister
}

// NewRepair creates a controller that periodically ensures that there is no stale data in OVN
func NewRepair(interval time.Duration,
	serviceLister corelisters.ServiceLister,
) *Repair {
	return &Repair{
		interval:      interval,
		serviceLister: serviceLister,
	}
}

// RunUntil starts the controller until the provided ch is closed.
func (r *Repair) RunUntil(stopCh <-chan struct{}) {
	wait.Until(func() {
		if err := r.RunOnce(); err != nil {
			klog.Errorf("Error repairing services: %v")
		}
	}, r.interval, stopCh)
}

// RunOnce verifies the state of Services and OVN LBs and returns an error if an unrecoverable problem occurs.
func (r *Repair) RunOnce() error {
	return retry.RetryOnConflict(retry.DefaultBackoff, r.runOnce)
}

// runOnce verifies the state of the cluster OVN LB VIP allocations and returns an error if an unrecoverable problem occurs.
func (r *Repair) runOnce() error {
	startTime := time.Now()
	klog.V(4).Infof("Starting repairing loop for services")
	defer func() {
		klog.V(4).Infof("Finished repairing loop for services: %v", time.Since(startTime))
		metrics.MetricSyncServiceLatency.WithLabelValues("repair-loop").Observe(time.Since(startTime).Seconds())
	}()

	// Obtain all the load balancers UUID
	ovnLBCache := make(map[v1.Protocol][]string)
	// We have different loadbalancers per protocol
	protocols := []v1.Protocol{v1.ProtocolSCTP, v1.ProtocolTCP, v1.ProtocolUDP}
	// ClusterIP OVN load balancers
	for _, p := range protocols {
		lbUUID, err := loadbalancer.GetOVNKubeLoadBalancer(p, loadbalancer.OvnLoadBalancerClusterIds)
		if err != nil {
			return errors.Wrapf(err, "Failed to get OVN load balancer for protocol %s", p)
		}
		ovnLBCache[p] = append(ovnLBCache[p], lbUUID)
	}
	// NodePort, ExternalIPs and Ingress OVN load balancers
	gatewayRouters, _, err := gateway.GetOvnGateways()
	if err != nil {
		return errors.Wrapf(err, "Failed to get gateway router")
	}
	for _, p := range protocols {
		for _, gatewayRouter := range gatewayRouters {
			lbUUID, err := gateway.GetGatewayLoadBalancer(gatewayRouter, p)
			if err != nil {
				return errors.Wrapf(err, "Failed to get OVN GR load balancer for protocol %s", p)
			}
			ovnLBCache[p] = append(ovnLBCache[p], lbUUID)
		}
	}

	// Get Kubernetes Service state
	svcVIPsProtocolMap := sets.NewString()
	services, err := r.serviceLister.List(labels.Everything())
	if err != nil {
		return errors.Wrapf(err, "Failed to list Services from the cache")
	}
	for _, svc := range services {
		for _, ip := range svc.Spec.ClusterIPs {
			for _, svcPort := range svc.Spec.Ports {
				vip := util.JoinHostPortInt32(ip, svcPort.Port)
				key := virtualIPKey(vip, svcPort.Protocol)
				svcVIPsProtocolMap.Insert(key)
			}
		}
	}

	// Reconcile with OVN state
	// Obtain all the VIPs present in the OVN LoadBalancers
	// and delete the ones that are not present in Kubernetes
	for _, p := range protocols {
		for _, lb := range ovnLBCache[p] {
			vips, err := loadbalancer.GetLoadBalancerVIPs(lb)
			if err != nil {
				return errors.Wrapf(err, "Failed to get load balancer vips for %s", ovnLBCache[p])
			}
			for vip := range vips {
				key := virtualIPKey(vip, p)
				// Virtual IP and protocol doesn't belong to a Kubernetes service
				if !svcVIPsProtocolMap.Has(key) {
					klog.Infof("Deleting non-existing Kubernetes vip %s from OVN load balancer %s", vip, ovnLBCache[p])
					if err := loadbalancer.DeleteLoadBalancerVIP(lb, vip); err != nil {
						return errors.Wrapf(err, "Failed to delete load balancer vips %s for %s", vip, ovnLBCache[p])
					}
				}
			}
		}
	}
	return nil
}
