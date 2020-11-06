package loadbalancer

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/util"
	"github.com/pkg/errors"

	kapi "k8s.io/api/core/v1"
	"k8s.io/klog"
)

// GetOVNKubeLoadBalancer returns the LoadBalancer matching the protocol
// in the OVN database using the external_ids = k8s-cluster-lb-${protocol}
func GetOVNKubeLoadBalancer(protocol kapi.Protocol) (string, error) {
	var out string
	var err error
	if protocol == kapi.ProtocolTCP {
		out, _, err = util.RunOVNNbctl("--data=bare",
			"--no-heading", "--columns=_uuid", "find", "load_balancer",
			"external_ids:k8s-cluster-lb-tcp=yes")
	} else if protocol == kapi.ProtocolUDP {
		out, _, err = util.RunOVNNbctl("--data=bare", "--no-heading",
			"--columns=_uuid", "find", "load_balancer",
			"external_ids:k8s-cluster-lb-udp=yes")
	} else if protocol == kapi.ProtocolSCTP {
		out, _, err = util.RunOVNNbctl("--data=bare", "--no-heading",
			"--columns=_uuid", "find", "load_balancer",
			"external_ids:k8s-cluster-lb-sctp=yes")
	}
	if err != nil {
		return "", err
	}
	if out == "" {
		return "", fmt.Errorf("no load balancer found in the database")
	}
	return out, nil
}

// GetLoadBalancerVIPs returns a map whose keys are VIPs (IP:port) on loadBalancer
func GetLoadBalancerVIPs(loadBalancer string) (map[string]interface{}, error) {
	outStr, _, err := util.RunOVNNbctl("--data=bare", "--no-heading",
		"get", "load_balancer", loadBalancer, "vips")
	if err != nil {
		return nil, err
	}
	if outStr == "" {
		return nil, fmt.Errorf("load balancer vips in OVN DB is an empty string")
	}
	// sample outStr:
	// - {"192.168.0.1:80"="10.1.1.1:80,10.2.2.2:80"}
	// - {"[fd01::]:80"="[fd02::]:80,[fd03::]:80"}
	outStrMap := strings.Replace(outStr, "=", ":", -1)

	var raw map[string]interface{}
	err = json.Unmarshal([]byte(outStrMap), &raw)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// DeleteLoadBalancerVIP removes the VIP as well as any reject ACLs associated to the LB
func DeleteLoadBalancerVIP(loadBalancer, vip string) error {
	vipQuotes := fmt.Sprintf("\"%s\"", vip)
	stdout, stderr, err := util.RunOVNNbctl("--if-exists", "remove", "load_balancer", loadBalancer, "vips", vipQuotes)
	if err != nil {
		// if we hit an error and fail to remove load balancer, we skip removing the rejectACL
		return fmt.Errorf("error in deleting load balancer vip %s for %s"+
			"stdout: %q, stderr: %q, error: %v",
			vip, loadBalancer, stdout, stderr, err)
	}
	return nil
}

// UpdateLoadBalancer updates the VIP for sourceIP:sourcePort to point to targets (an
// array of IP:port strings)
func UpdateLoadBalancer(lb, sourceIP string, sourcePort int32, targets []string) error {
	vip := util.JoinHostPortInt32(sourceIP, sourcePort)
	lbTarget := fmt.Sprintf(`vips:"%s"="%s"`, vip, strings.Join(targets, ","))

	out, stderr, err := util.RunOVNNbctl("set", "load_balancer", lb, lbTarget)
	if err != nil {
		return fmt.Errorf("error in configuring load balancer: %s "+
			"stdout: %q, stderr: %q, error: %v", lb, out, stderr, err)
	}

	return nil
}

// GetLogicalRoutersForLoadBalancer get the switches associated to a LoadBalancer
func GetLogicalSwitchesForLoadBalancer(lb string) ([]string, error) {
	out, _, err := util.RunOVNNbctl("--data=bare", "--no-heading",
		"--columns=_uuid", "find",
		"logical_switch", fmt.Sprintf("load_balancer{>=}%s", lb))
	if err != nil {
		return nil, err
	}
	if len(strings.Fields(out)) > 0 {
		return strings.Fields(out), nil
	}
	return nil, nil
}

// GetLogicalRoutersForLoadBalancer get the routers associated to a LoadBalancer
func GetLogicalRoutersForLoadBalancer(lb string) ([]string, error) {
	out, _, err := util.RunOVNNbctl("--data=bare", "--no-heading",
		"--columns=name", "find",
		"logical_router", fmt.Sprintf("load_balancer{>=}%s", lb))
	if err != nil {
		return nil, err
	}
	if len(strings.Fields(out)) > 0 {
		return strings.Fields(out), nil
	}

	return nil, nil
}

// GenerateACLName generates a deterministic ACL name based on the load_balancer parameters
func GenerateACLName(lb string, sourceIP string, sourcePort int32) string {
	aclName := fmt.Sprintf("%s-%s:%d", lb, sourceIP, sourcePort)
	// ACL names are limited to 63 characters
	if len(aclName) > 63 {
		var ipPortLen int
		srcPortStr := fmt.Sprintf("%d", sourcePort)
		// Add the length of the IP (max 15 with periods, max 39 with colons),
		// plus length of sourcePort (max 5 char),
		// plus 1 for additional ':' to separate,
		// plus 1 for '-' between lb and IP.
		// With full IPv6 address and 5 char port, max ipPortLen is 62
		// With full IPv4 address and 5 char port, max ipPortLen is 24.
		ipPortLen = len(sourceIP) + len(srcPortStr) + 1 + 1
		lbTrim := 63 - ipPortLen
		// Shorten the Load Balancer name to allow full IP:port
		tmpLb := lb[:lbTrim]
		klog.Infof("Limiting ACL Name from %s to %s-%s:%d to keep under 63 characters", aclName, tmpLb, sourceIP, sourcePort)
		aclName = fmt.Sprintf("%s-%s:%d", tmpLb, sourceIP, sourcePort)
	}
	return aclName
}

// GenerateACLNameForOVNCommand sanitize the ACL name because the generateACLName
// function was including backslash escapes for the ACL
// name for use in OVN commands that have trouble with literal ":". That
// was causing a mismatch when services were syncing because the name
// actually returned from an OVN command does not include any backslashes
// so the names would not match. #1749
func GenerateACLNameForOVNCommand(lb string, sourceIP string, sourcePort int32) string {
	return strings.ReplaceAll(GenerateACLName(lb, sourceIP, sourcePort), ":", "\\:")
}

// GetACLByName returns the ACL UUID
func GetACLByName(aclName string) (string, error) {
	aclUUID, stderr, err := util.RunOVNNbctl("--data=bare", "--no-heading", "--columns=_uuid", "find", "acl",
		fmt.Sprintf("name=%s", aclName))
	if err != nil {
		return "", errors.Wrapf(err, "Error while querying ACLs by name: %s", stderr)
	} else if len(aclUUID) == 0 {
		return "", fmt.Errorf("ACL not found: %s", aclName)
	}
	return aclUUID, nil
}

// Remove the ACL uuid entry from Logical Switch acl's list.
// Deprecated:This method is not required once a release with this patch is out.
// This logic is specifically added to address the ovn upgrade.
func RemoveACLFromNodeSwitches(switches []string, aclUUID string) {
	args := []string{}
	for _, ls := range switches {
		args = append(args, "--", "--if-exists", "remove", "logical_switch", ls, "acl", aclUUID)
	}

	if len(args) > 0 {
		_, _, err := util.RunOVNNbctl(args...)
		if err != nil {
			klog.Errorf("Error while removing ACL: %s, from switches, error: %v", aclUUID, err)
		} else {
			klog.Infof("ACL: %s, removed from switches: %s", aclUUID, switches)
		}
	}
}

// RemoveACLFromPortGroup removes the ACL from the port-group
func RemoveACLFromPortGroup(lb, aclUUID, clusterPortGroupUUID string) {
	_, stderr, err := util.RunOVNNbctl("--", "--if-exists", "remove", "port_group", clusterPortGroupUUID, "acls", aclUUID)
	if err != nil {
		klog.Errorf("Failed to remove reject ACL %s from LB %s: stderr: %q, error: %v", aclUUID, lb, stderr, err)
	} else {
		klog.Infof("ACL: %s, removed from the port group : %s", aclUUID, clusterPortGroupUUID)
	}
}
