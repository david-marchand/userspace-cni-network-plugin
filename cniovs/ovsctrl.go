// Copyright 2018-2020 Red Hat, Intel Corp.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cniovs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/intel/userspace-cni-network-plugin/logging"
)

const defaultOvSSocketDir = "/usr/local/var/run/openvswitch/"

/*
OVS command execution handling and its public interface
*/

type ExecCommandInterface interface {
	execCommand(cmd string, args []string) ([]byte, error)
}

type realExecCommand struct{}

func (e *realExecCommand) execCommand(cmd string, args []string) ([]byte, error) {
	return exec.Command(cmd, args...).Output()
}

var ovsCommand ExecCommandInterface = &realExecCommand{}

func SetExecCommand(o ExecCommandInterface) {
	ovsCommand = o
}

func SetDefaultExecCommand() {
	ovsCommand = &realExecCommand{}
}

func execCommand(cmd string, args []string) ([]byte, error) {
	return ovsCommand.execCommand(cmd, args)
}

/*
Functions to control OVS by using the ovs-vsctl cmdline client.
*/

func createVhostPort(sock_dir string, vhost_name string, sock_name string, client bool, bridge_name string) (error) {
	var err error

	type_str := "type=dpdkvhostuser"
	if client {
		type_str = "type=dpdkvhostuserclient"
	}

	// COMMAND: ovs-vsctl add-port <bridge_name> <vhost_name> -- set Interface <vhost_name> type=<dpdkvhostuser|dpdkvhostuserclient>
	cmd := "ovs-vsctl"
	args := []string{"add-port", bridge_name, vhost_name, "--", "set", "Interface", vhost_name, type_str}

	if client {
		socketarg := "options:vhost-server-path=" + filepath.Join(sock_dir, sock_name)
		logging.Debugf("Additional string: %s", socketarg)

		args = append(args, socketarg)
	}

	if _, err = execCommand(cmd, args); err != nil {
		return err
	}

	if !client {
		// Determine the location OvS uses for Sockets. Default location can be
		// overwritten with environmental variable: OVS_SOCKDIR
		ovs_socket_dir, ok := os.LookupEnv("OVS_SOCKDIR")
		if !ok {
			ovs_socket_dir = defaultOvSSocketDir
		}

		// Move socket to defined dir for easier mounting
		err = os.Rename(filepath.Join(ovs_socket_dir, sock_name), filepath.Join(sock_dir, sock_name))
		if err != nil {
			_ = logging.Errorf("Rename ERROR: %v", err)
			err = nil

			//deleteVhostPort(sock_name, bridge_name)
		}
	}

	return err
}

func deleteVhostPort(sock_name string, bridge_name string) error {
	// COMMAND: ovs-vsctl del-port <bridge_name> <sock_name>
	cmd := "ovs-vsctl"
	args := []string{"--if-exists", "del-port", bridge_name, sock_name}
	_, err := execCommand(cmd, args)
	logging.Verbosef("ovsctl.deleteVhostPort(): return=%v", err)
	return err
}

func createBridge(bridge_name string) error {
	// COMMAND: ovs-vsctl add-br <bridge_name> -- set bridge <bridge_name> datapath_type=netdev
	cmd := "ovs-vsctl"
	args := []string{"add-br", bridge_name, "--", "set", "bridge", bridge_name, "datapath_type=netdev"}
	_, err := execCommand(cmd, args)
	logging.Verbosef("ovsctl.createBridge(): return=%v", err)
	return err
}

func linkBridge(bridge_name string, vlan_id int) error {
	cmd := "ovs-vsctl"
	args := []string{"add-port", bridge_name, bridge_name + "-to-br-phys", "--", "set", "interface", bridge_name + "-to-br-phys", "type=patch", "options:peer=br-phys-to-" + bridge_name}
	_, err := execCommand(cmd, args)
	logging.Verbosef("ovsctl.linkBridge(): " + bridge_name + "-to-br-phys return=%v", err)

	if err == nil {
		args := []string{"add-port", "br-phys", "br-phys-to-" + bridge_name, "--", "set", "interface", "br-phys-to-" + bridge_name, "type=patch", "options:peer=" + bridge_name + "-to-br-phys", "--", "set", "port", "br-phys-to-" + bridge_name, fmt.Sprintf("tag=%d", vlan_id)}
		_, err := execCommand(cmd, args)
		logging.Verbosef("ovsctl.linkBridge(): br-phys-to-" + bridge_name + " return=%v", err)
	}

	return err
}

func unlinkBridge(bridge_name string) error {
	cmd := "ovs-vsctl"
	args := []string{"del-port", "br-phys", "br-phys-to-" + bridge_name}
	_, err := execCommand(cmd, args)
	logging.Verbosef("ovsctl.unlinkBridge(): br-phys-to-" + bridge_name + " return=%v", err)

	return err
}

func configL2Bridge(bridge_name string) error {
	// COMMAND: ovs-ofctl add-flow <bridge_name> actions=NORMAL
	cmd := "ovs-ofctl"
	args := []string{"add-flow", bridge_name, "actions=NORMAL"}
	_, err := execCommand(cmd, args)
	logging.Verbosef("ovsctl.configL2Bridge(): return=%v", err)
	return err
}

func deleteBridge(bridge_name string) error {
	// COMMAND: ovs-vsctl del-br <bridge_name>
	cmd := "ovs-vsctl"
	args := []string{"del-br", bridge_name}

	_, err := execCommand(cmd, args)
	logging.Verbosef("ovsctl.deleteBridge(): return=%v", err)
	return err
}

func getVhostPortMac(sock_name string) (string, error) {
	// COMMAND: ovs-vsctl --bare --columns=mac find port name=<sock_name>
	cmd := "ovs-vsctl"
	args := []string{"--bare", "--columns=mac", "find", "port", "name=" + sock_name}
	if mac_b, err := execCommand(cmd, args); err != nil {
		return "", err
	} else {
		return strings.Replace(string(mac_b), "\n", "", -1), nil
	}
}

func findBridge(bridge_name string) bool {
	found := false

	// COMMAND: ovs-vsctl --bare --columns=name find bridge name=<bridge_name>
	cmd := "ovs-vsctl"
	args := []string{"--bare", "--columns=name", "find", "bridge", "name=" + bridge_name}
	//if name, err := execCommand(cmd, args); err != nil {
	name, err := execCommand(cmd, args)
	logging.Verbosef("ovsctl.findBridge(): return  name=%v err=%v", name, err)
	if err == nil {
		if len(name) != 0 {
			found = true
		}
	}

	return found
}

func doesBridgeContainInterfaces(bridge_name string) bool {
	found := false

	// ovs-vsctl list-ports <bridge_name>
	cmd := "ovs-vsctl"
	args := []string{"list-ports", bridge_name}
	//if name, err := execCommand(cmd, args); err != nil {
	name, err := execCommand(cmd, args)
	logging.Verbosef("ovsctl.doesBridgeContainInterfaces(): return  name=%v err=%v", name, err)
	if err == nil {
		portToSkip := bridge_name + "-to-br-phys"
		ports := strings.Split(strings.TrimSpace(string(name)), "\n")
		for _, port := range ports {
			port = strings.TrimSpace(port)
			logging.Debugf("ovsctl.doesBridgeContainInterfaces(): port %s, portToSkip=%s", port, portToSkip)
			if port != "" && port != portToSkip {
				found = true
				break
			}
		}
	}

	return found
}

// setIngressPolicing sets ingress policing on an OVS interface to limit
// traffic FROM the container (container egress). Rate is in bits/sec, burst is in bits.
// OVS uses kbps for rate and kb for burst.
func setIngressPolicing(portName string, rateBits uint64, burstBits uint64) error {
	// Convert bits/sec to kbps and bits to kb
	rateKbps := rateBits / 1000
	burstKb := burstBits / 1000

	// COMMAND: ovs-vsctl set Interface <port> ingress_policing_rate=<kbps> ingress_policing_burst=<kb>
	cmd := "ovs-vsctl"
	args := []string{
		"set", "Interface", portName,
		fmt.Sprintf("ingress_policing_rate=%d", rateKbps),
		fmt.Sprintf("ingress_policing_burst=%d", burstKb),
	}
	_, err := execCommand(cmd, args)
	logging.Verbosef("ovsctl.setIngressPolicing(): port=%s rate=%d kbps burst=%d kb return=%v",
		portName, rateKbps, burstKb, err)
	return err
}

// clearIngressPolicing removes ingress policing from an OVS interface.
func clearIngressPolicing(portName string) error {
	// COMMAND: ovs-vsctl set Interface <port> ingress_policing_rate=0 ingress_policing_burst=0
	cmd := "ovs-vsctl"
	args := []string{
		"set", "Interface", portName,
		"ingress_policing_rate=0",
		"ingress_policing_burst=0",
	}
	_, err := execCommand(cmd, args)
	logging.Verbosef("ovsctl.clearIngressPolicing(): port=%s return=%v", portName, err)
	return err
}

// setEgressPolicer sets an egress-policer QoS on an OVS port to limit
// traffic TO the container (container ingress). Rate is in bits/sec, burst is in bits.
// OVS egress-policer uses bytes/sec for cir and bytes for cbs.
func setEgressPolicer(portName string, rateBits uint64, burstBits uint64) error {
	// Convert bits to bytes
	rateBytes := rateBits / 8
	burstBytes := burstBits / 8

	// COMMAND: ovs-vsctl set port <port> qos=@newqos -- \
	//   --id=@newqos create qos type=egress-policer other-config:cir=<bytes/sec> other-config:cbs=<bytes>
	cmd := "ovs-vsctl"
	args := []string{
		"set", "port", portName, "qos=@newqos", "--",
		"--id=@newqos", "create", "qos", "type=egress-policer",
		fmt.Sprintf("other-config:cir=%d", rateBytes),
		fmt.Sprintf("other-config:cbs=%d", burstBytes),
	}
	_, err := execCommand(cmd, args)
	logging.Verbosef("ovsctl.setEgressPolicer(): port=%s cir=%d bytes/sec cbs=%d bytes return=%v",
		portName, rateBytes, burstBytes, err)
	return err
}

// clearEgressPolicer removes egress-policer QoS from an OVS port and deletes the QoS record.
func clearEgressPolicer(portName string) error {
	// First, get the QoS UUID attached to the port
	cmd := "ovs-vsctl"
	args := []string{"--if-exists", "get", "Port", portName, "qos"}
	output, err := execCommand(cmd, args)
	if err != nil {
		logging.Verbosef("ovsctl.clearEgressPolicer(): failed to get qos for port=%s: %v", portName, err)
		return err
	}

	qosUUID := strings.TrimSpace(string(output))
	if qosUUID == "" || qosUUID == "[]" {
		logging.Verbosef("ovsctl.clearEgressPolicer(): port=%s has no QoS attached", portName)
		return nil
	}

	// Destroy QoS record and clear reference from port in a single atomic command
	args = []string{"--", "destroy", "QoS", qosUUID, "--", "clear", "Port", portName, "qos"}
	_, err = execCommand(cmd, args)
	logging.Verbosef("ovsctl.clearEgressPolicer(): port=%s qos=%s return=%v", portName, qosUUID, err)
	return err
}
