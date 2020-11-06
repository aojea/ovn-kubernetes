module github.com/ovn-org/ovn-kubernetes/go-controller

go 1.13

require (
	github.com/Mellanox/sriovnet v0.0.0-20190516174650-73402dc8fcaa
	github.com/Microsoft/hcsshim v0.8.10-0.20200606013352-27a858bf1651
	github.com/bhendo/go-powershell v0.0.0-20190719160123-219e7fb4e41e
	github.com/cenk/hub v1.0.1 // indirect
	github.com/containernetworking/cni v0.8.0
	github.com/containernetworking/plugins v0.8.7
	github.com/coreos/go-iptables v0.4.5
	github.com/ebay/go-ovn v0.1.0
	github.com/ebay/libovsdb v0.2.1-0.20200719163122-3332afaeb27c
	github.com/gorilla/mux v1.7.3
	github.com/hashicorp/golang-lru v0.5.3 // indirect
	github.com/imdario/mergo v0.3.8 // indirect
	github.com/juju/errors v0.0.0-20200330140219-3fe23663418f // indirect
	github.com/juju/testing v0.0.0-20200608005635-e4eedbc6f7aa // indirect
	github.com/k8snetworkplumbingwg/network-attachment-definition-client v0.0.0-20200626054723-37f83d1996bc
	github.com/onsi/ginkgo v1.11.0
	github.com/onsi/gomega v1.8.1
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.2.1
	github.com/satori/go.uuid v0.0.0-20181028125025-b2ce2384e17b // indirect
	github.com/spf13/afero v1.2.2
	github.com/stretchr/testify v1.4.0
	github.com/urfave/cli/v2 v2.2.0
	github.com/vishvananda/netlink v0.0.0-20200625175047-bca67dfc8220
	golang.org/x/sys v0.0.0-20200622214017-ed371f2e16b4
	gopkg.in/fsnotify/fsnotify.v1 v1.4.7
	gopkg.in/gcfg.v1 v1.2.3
	gopkg.in/natefinch/lumberjack.v2 v2.0.0
	gopkg.in/warnings.v0 v0.1.2 // indirect
	k8s.io/api v0.20.0-beta.1
	k8s.io/apiextensions-apiserver v0.20.0-beta.1
	k8s.io/apimachinery v0.20.0-beta.1
	k8s.io/client-go v0.20.0-beta.1
	k8s.io/klog v1.0.0
	k8s.io/klog/v2 v2.4.0 // indirect
	k8s.io/kube-openapi v0.0.0-20201104192653-842b07581b16 // indirect
	k8s.io/utils v0.0.0-20201104234853-8146046b121e
)

replace (
	github.com/ebay/go-ovn v0.1.0 => github.com/ebay/go-ovn v0.1.1-0.20200810162212-30abed5fb968
	k8s.io/api => k8s.io/api v0.20.0-beta.1
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.20.0-beta.1
	k8s.io/apimachinery => k8s.io/apimachinery v0.20.0-beta.1
	k8s.io/client-go => k8s.io/client-go v0.20.0-beta.1
)
