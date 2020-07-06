#!/usr/bin/env bash

set -ex

SHARD=$1

pushd $GOPATH/src/k8s.io/kubernetes/
export KUBERNETES_CONFORMANCE_TEST=y
export KUBECONFIG=${HOME}/admin.conf
export MASTER_NAME=${KIND_CLUSTER_NAME}-control-plane
export NODE_NAMES=${MASTER_NAME}

SKIPPED_TESTS="
# PERFORMANCE TESTS: NOT WANTED FOR CI
Networking IPerf IPv[46]
\[Feature:PerformanceDNS\]

# FEATURES NOT AVAILABLE IN OUR CI ENVIRONMENT
\[Feature:Networking-IPv6\]
\[Feature:Federation\]

# TESTS THAT ASSUME KUBE-PROXY
kube-proxy
should set TCP CLOSE_WAIT timeout

# TO BE IMPLEMENTED: https://github.com/ovn-org/ovn-kubernetes/issues/1142
\[Feature:IPv6DualStackAlphaFeature\]

# TO BE IMPLEMENTED: https://github.com/ovn-org/ovn-kubernetes/issues/819
Services.+session affinity

# TO BE IMPLEMENTED: https://github.com/ovn-org/ovn-kubernetes/issues/1116
EndpointSlices

# NOT IMPLEMENTED; SEE DISCUSSION IN https://github.com/ovn-org/ovn-kubernetes/pull/1225
named port.+\[Feature:NetworkPolicy\]

# ???
\[Feature:NoSNAT\]
Services.+(ESIPP|cleanup finalizer)
configMap nameserver
ClusterDns \[Feature:Example\]
should set default value on new IngressClass
# RACE CONDITION IN TEST, SEE https://github.com/kubernetes/kubernetes/pull/90254
should prevent Ingress creation if more than 1 IngressClass marked as default
"

SKIPPED_TESTS=$(echo "${SKIPPED_TESTS}" | sed -e '/^\($\|#\)/d' -e 's/ /\\s/g' | tr '\n' '|' | sed -e 's/|$//')

GINKGO_ARGS="--num-nodes=2 --disable-log-dump=false --report-dir=${E2E_REPORT_DIR} --report-prefix=${E2E_REPORT_PREFIX}"

# if we set PARALLEL=true, skip serial tests set --ginkgo-parallel
if [ "${PARALLEL:-true}" = "true" ]; then
  GINKGO_ARGS+=("--nodes=25")
  SKIPPED_TESTS="${SKIPPED_TESTS}|\\[Serial\\]"
fi

case "$SHARD" in
	shard-network)
		# all tests that don't have P as their sixth letter after the N, and all other tests
		GINKGO_ARGS="${GINKGO_ARGS} --ginkgo.focus=\\[sig-network\\] --ginkgo.skip=${SKIPPED_TESTS}"
		;;
	shard-conformance)
		# all conformance but serial
		GINKGO_ARGS="${GINKGO_ARGS} --ginkgo.focus=\\[Conformance\\] --ginkgo.skip=${SKIPPED_TESTS}"
		;;
	shard-conformance)
		# all conformance
		GINKGO_ARGS="${GINKGO_ARGS} --ginkgo.focus=\\[Conformance\\] --ginkgo.skip=${SKIPPED_TESTS}"
		;;
	shard-test)
		TEST_REGEX_REPR=$(echo ${@:2} | sed 's/ /\\s/g')
		GINKGO_ARGS="${GINKGO_ARGS} --ginkgo.focus=$TEST_REGEX_REPR --ginkgo.skip=${SKIPPED_TESTS}"
		;;
	*)
		echo "unknown shard"
		exit 1
	;;
esac

e2e.test ${GINKGO_ARGS}

