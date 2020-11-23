package services

import (
	"testing"

	ovntest "github.com/ovn-org/ovn-kubernetes/go-controller/pkg/testing"
	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/util"
	v1 "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	utilpointer "k8s.io/utils/pointer"
)

const (
	tcpLBUUID  string = "1a3dfc82-2749-4931-9190-c30e7c0ecea3"
	udpLBUUID  string = "6d3142fc-53e8-4ac1-88e6-46094a5a9957"
	sctpLBUUID string = "0514c521-a120-4756-aec6-883fe5db7139"
)

type repairController struct {
	*Repair
	serviceStore       cache.Store
	endpointSliceStore cache.Store
}

func newFakeRepair() *repairController {
	client := fake.NewSimpleClientset()
	informerFactory := informers.NewSharedInformerFactory(client, 0)
	eventBroadcaster := record.NewBroadcaster()
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, v1.EventSource{Component: "repair-services"})

	repair := &Repair{
		interval:            0,
		serviceLister:       informerFactory.Core().V1().Services().Lister(),
		endpointSliceLister: informerFactory.Discovery().V1beta1().EndpointSlices().Lister(),
		recorder:            recorder,
	}
	return &repairController{
		repair,
		informerFactory.Core().V1().Services().Informer().GetStore(),
		informerFactory.Discovery().V1beta1().EndpointSlices().Informer().GetStore(),
	}
}

func TestRepair_NoUpdateEmpty(t *testing.T) {
	r := newFakeRepair()
	// Expected OVN commands
	fexec := ovntest.NewFakeExec()
	initializeClusterIPLBs(fexec)
	// OVN is empty
	fexec.AddFakeCmd(&ovntest.ExpectedCmd{
		Cmd:    "ovn-nbctl --timeout=15 --data=bare --no-heading get load_balancer " + sctpLBUUID + " vips",
		Output: "",
	})
	fexec.AddFakeCmd(&ovntest.ExpectedCmd{
		Cmd:    "ovn-nbctl --timeout=15 --data=bare --no-heading get load_balancer " + tcpLBUUID + " vips",
		Output: "",
	})
	fexec.AddFakeCmd(&ovntest.ExpectedCmd{
		Cmd:    "ovn-nbctl --timeout=15 --data=bare --no-heading get load_balancer " + udpLBUUID + " vips",
		Output: "",
	})

	err := util.SetExec(fexec)
	if err != nil {
		t.Errorf("fexec error: %v", err)
	}

	if err := r.runOnce(); err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestRepair_OVNStaleData(t *testing.T) {
	r := newFakeRepair()
	// Expected OVN commands
	fexec := ovntest.NewFakeExec()
	initializeClusterIPLBs(fexec)
	// There are remaining OVN LB that doesn't exist in Kubernetes
	fexec.AddFakeCmd(&ovntest.ExpectedCmd{
		Cmd:    "ovn-nbctl --timeout=15 --data=bare --no-heading get load_balancer " + sctpLBUUID + " vips",
		Output: "",
	})
	fexec.AddFakeCmd(&ovntest.ExpectedCmd{
		Cmd:    "ovn-nbctl --timeout=15 --data=bare --no-heading get load_balancer " + tcpLBUUID + " vips",
		Output: "",
	})
	fexec.AddFakeCmd(&ovntest.ExpectedCmd{
		Cmd:    "ovn-nbctl --timeout=15 --data=bare --no-heading get load_balancer " + udpLBUUID + " vips",
		Output: `{"10.96.0.10:53"="10.244.2.3:53,10.244.2.5:53", "10.96.0.10:9153"="10.244.2.3:9153,10.244.2.5:9153", "10.96.0.1:443"="172.19.0.3:6443"}`,
	})
	// The repair loop must delete the remaining entries in OVN
	fexec.AddFakeCmd(&ovntest.ExpectedCmd{
		Cmd:    "ovn-nbctl --timeout=15 --if-exists remove load_balancer " + udpLBUUID + " vips \"10.96.0.10:53\"",
		Output: "",
	})
	fexec.AddFakeCmd(&ovntest.ExpectedCmd{
		Cmd:    "ovn-nbctl --timeout=15 --if-exists remove load_balancer " + udpLBUUID + " vips \"10.96.0.10:9153\"",
		Output: "",
	})
	fexec.AddFakeCmd(&ovntest.ExpectedCmd{
		Cmd:    "ovn-nbctl --timeout=15 --if-exists remove load_balancer " + udpLBUUID + " vips \"10.96.0.1:443\"",
		Output: "",
	})
	// The repair loop must delete them
	err := util.SetExec(fexec)
	if err != nil {
		t.Errorf("fexec error: %v", err)
	}

	if err := r.runOnce(); err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestRepair_OVNSynced(t *testing.T) {
	r := newFakeRepair()
	// Expected OVN commands
	fexec := ovntest.NewFakeExec()
	initializeClusterIPLBs(fexec)

	svc, slices := createServiceWithEndpoints("svcfoo", "nsbar", []string{"10.96.0.10", "fd00:96::1"})
	for _, s := range slices {
		s := s
		r.endpointSliceStore.Add(&s)
	}
	r.serviceStore.Add(svc)
	// OVN is missing some of the data in Kubernetes
	fexec.AddFakeCmd(&ovntest.ExpectedCmd{
		Cmd:    "ovn-nbctl --timeout=15 --data=bare --no-heading get load_balancer " + sctpLBUUID + " vips",
		Output: "",
	})
	fexec.AddFakeCmd(&ovntest.ExpectedCmd{
		Cmd:    "ovn-nbctl --timeout=15 --data=bare --no-heading get load_balancer " + tcpLBUUID + " vips",
		Output: `{"10.96.0.10:80"="10.0.0.2:3456,10.0.0.3:3456", "[fd00:96::1]:80"="[2001:db8::1]:3456,[2001:db8::2]:3456"}`,
	})
	fexec.AddFakeCmd(&ovntest.ExpectedCmd{
		Cmd:    "ovn-nbctl --timeout=15 --data=bare --no-heading get load_balancer " + udpLBUUID + " vips",
		Output: "",
	})
	// The repair loop must create the missing service
	fexec.AddFakeCmd(&ovntest.ExpectedCmd{
		Cmd:    "ovn-nbctl --timeout=15 --if-exists remove load_balancer " + udpLBUUID + " vips \"10.96.0.10:9153\"",
		Output: "",
	})
	fexec.AddFakeCmd(&ovntest.ExpectedCmd{
		Cmd:    "ovn-nbctl --timeout=15 --if-exists remove load_balancer " + udpLBUUID + " vips \"10.96.0.10:53\"",
		Output: "",
	})
	fexec.AddFakeCmd(&ovntest.ExpectedCmd{
		Cmd:    "ovn-nbctl --timeout=15 --if-exists remove load_balancer " + udpLBUUID + " vips \"10.96.0.1:443\"",
		Output: "",
	})

	err := util.SetExec(fexec)
	if err != nil {
		t.Errorf("fexec error: %v", err)
	}

	if err := r.runOnce(); err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestRepair_OVNMissingService(t *testing.T) {
	r := newFakeRepair()
	// Expected OVN commands
	fexec := ovntest.NewFakeExec()
	initializeClusterIPLBs(fexec)

	svc, slices := createServiceWithEndpoints("svcfoo", "nsbar", []string{"10.96.0.10", "fd00:96::1"})
	for _, s := range slices {
		s := s
		r.endpointSliceStore.Add(&s)
	}
	r.serviceStore.Add(svc)
	// OVN is missing some of the data in Kubernetes
	fexec.AddFakeCmd(&ovntest.ExpectedCmd{
		Cmd:    "ovn-nbctl --timeout=15 --data=bare --no-heading get load_balancer " + sctpLBUUID + " vips",
		Output: "",
	})
	fexec.AddFakeCmd(&ovntest.ExpectedCmd{
		Cmd:    "ovn-nbctl --timeout=15 --data=bare --no-heading get load_balancer " + tcpLBUUID + " vips",
		Output: `{"10.96.0.10:80"="10.0.0.2:3456,10.0.0.3:3456"}`,
	})
	fexec.AddFakeCmd(&ovntest.ExpectedCmd{
		Cmd:    "ovn-nbctl --timeout=15 --data=bare --no-heading get load_balancer " + udpLBUUID + " vips",
		Output: "",
	})
	// The repair loop must create the missing service
	fexec.AddFakeCmd(&ovntest.ExpectedCmd{
		Cmd:    "ovn-nbctl --timeout=15 set load_balancer " + tcpLBUUID + " vips:\"[fd00:96::1]:80\"=\"[2001:db8::1]:3456,[2001:db8::2]:3456\"",
		Output: "",
	})
	err := util.SetExec(fexec)
	if err != nil {
		t.Errorf("fexec error: %v", err)
	}

	if err := r.runOnce(); err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestRepair_OVNMissingEndpoint(t *testing.T) {
	r := newFakeRepair()
	// Expected OVN commands
	fexec := ovntest.NewFakeExec()
	initializeClusterIPLBs(fexec)

	svc, slices := createServiceWithEndpoints("svcfoo", "nsbar", []string{"10.96.0.10", "fd00:96::1"})
	r.serviceStore.Add(svc)
	for _, s := range slices {
		s := s
		r.endpointSliceStore.Add(&s)
	}
	// OVN is missing some of the data in Kubernetes
	fexec.AddFakeCmd(&ovntest.ExpectedCmd{
		Cmd:    "ovn-nbctl --timeout=15 --data=bare --no-heading get load_balancer " + sctpLBUUID + " vips",
		Output: "",
	})
	fexec.AddFakeCmd(&ovntest.ExpectedCmd{
		Cmd:    "ovn-nbctl --timeout=15 --data=bare --no-heading get load_balancer " + tcpLBUUID + " vips",
		Output: `{"10.96.0.10:80"="10.0.0.2:3456,10.0.0.3:3456","[fd00:96::1]:80"="[2001:db8::1]:3456"}`,
	})
	fexec.AddFakeCmd(&ovntest.ExpectedCmd{
		Cmd:    "ovn-nbctl --timeout=15 --data=bare --no-heading get load_balancer " + udpLBUUID + " vips",
		Output: "",
	})
	// The repair loop must create the missing service with all the endpoints
	fexec.AddFakeCmd(&ovntest.ExpectedCmd{
		Cmd:    "ovn-nbctl --timeout=15 set load_balancer " + tcpLBUUID + " vips:\"[fd00:96::1]:80\"=\"[2001:db8::1]:3456,[2001:db8::2]:3456\"",
		Output: "",
	})
	err := util.SetExec(fexec)
	if err != nil {
		t.Errorf("fexec error: %v", err)
	}

	if err := r.runOnce(); err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
}

func initializeClusterIPLBs(fexec *ovntest.FakeExec) {
	fexec.AddFakeCmd(&ovntest.ExpectedCmd{
		Cmd:    "ovn-nbctl --timeout=15 --data=bare --no-heading --columns=_uuid find load_balancer external_ids:k8s-cluster-lb-sctp=yes",
		Output: sctpLBUUID,
	})

	fexec.AddFakeCmd(&ovntest.ExpectedCmd{
		Cmd:    "ovn-nbctl --timeout=15 --data=bare --no-heading --columns=_uuid find load_balancer external_ids:k8s-cluster-lb-tcp=yes",
		Output: tcpLBUUID,
	})
	fexec.AddFakeCmd(&ovntest.ExpectedCmd{
		Cmd:    "ovn-nbctl --timeout=15 --data=bare --no-heading --columns=_uuid find load_balancer external_ids:k8s-cluster-lb-udp=yes",
		Output: udpLBUUID,
	})
}

func createServiceWithEndpoints(name, ns string, clusterIPs []string) (*v1.Service, []discovery.EndpointSlice) {
	svc := v1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: v1.ServiceSpec{
			Type:       v1.ServiceTypeClusterIP,
			ClusterIP:  clusterIPs[0],
			ClusterIPs: clusterIPs,
			Selector:   map[string]string{"foo": "bar"},
			Ports: []v1.ServicePort{{
				Port:     80,
				Protocol: v1.ProtocolTCP,
			}},
		},
	}
	sliceIPv4 := discovery.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "ab23",
			Namespace: ns,
			Labels:    map[string]string{discovery.LabelServiceName: name},
		},
		Ports: []discovery.EndpointPort{
			{
				Name:     utilpointer.StringPtr("tcp-example"),
				Protocol: protoPtr(v1.ProtocolTCP),
				Port:     utilpointer.Int32Ptr(int32(3456)),
			},
		},
		AddressType: discovery.AddressTypeIPv4,
		Endpoints: []discovery.Endpoint{
			{
				Conditions: discovery.EndpointConditions{
					Ready: utilpointer.BoolPtr(true),
				},
				Addresses: []string{"10.0.0.2"},
				Topology:  map[string]string{"kubernetes.io/hostname": "node-1"},
			},
			{
				Conditions: discovery.EndpointConditions{
					Ready: utilpointer.BoolPtr(true),
				},
				Addresses: []string{"10.0.0.3"},
				Topology:  map[string]string{"kubernetes.io/hostname": "node-2"},
			},
		},
	}
	sliceIPv6 := discovery.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "cd0f",
			Namespace: ns,
			Labels:    map[string]string{discovery.LabelServiceName: name},
		},
		Ports: []discovery.EndpointPort{
			{
				Name:     utilpointer.StringPtr("tcp-example"),
				Protocol: protoPtr(v1.ProtocolTCP),
				Port:     utilpointer.Int32Ptr(int32(3456)),
			},
		},
		AddressType: discovery.AddressTypeIPv6,
		Endpoints: []discovery.Endpoint{
			{
				Conditions: discovery.EndpointConditions{
					Ready: utilpointer.BoolPtr(true),
				},
				Addresses: []string{"2001:db8::1"},
				Topology:  map[string]string{"kubernetes.io/hostname": "node-1"},
			},
			{
				Conditions: discovery.EndpointConditions{
					Ready: utilpointer.BoolPtr(true),
				},
				Addresses: []string{"2001:db8::2"},
				Topology:  map[string]string{"kubernetes.io/hostname": "node-2"},
			},
		},
	}
	return &svc, []discovery.EndpointSlice{sliceIPv4, sliceIPv6}
}
