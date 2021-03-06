package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/containernetworking/cni/pkg/invoke"
	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/cni/pkg/version"
	"github.com/markstgodard/go-keystone/keystone"
	"github.com/markstgodard/go-neutron/neutron"
)

// CNI plugin which uses Neutron API for control plane (networks,subnets,ports)
// and uses Keystone API for authentication.
// This plugin is also Cloud Foundry aware in that it will use the `space_id`
// to automatically created a space-based network/subnet. Cloud Foundry info
// is passed to this plugin via Garden runC (garden external networker).
// This plugin will delegate to another CNI plugin such as OVS for setting up
// the virtual network interface.
// IP address created from Neutron create port will be passed to delegate CNI
// plugin via a runtime updated `delegate` which adds the `ip` and `cidr` property.
// (i.e. "ip: "10.0.1.10", "cidr": "10.0.1.10/32" )
// Example CNI Plugin config:
/*
{
	"cniVersion": "0.2.0",
  "name": "cni-neutron-ovs",
  "type": "gofer",
	"neutron_url": "https://somehost:9696",
	"keystone_url": "https://somehost:5000",
	"keystone_user": "admin",
	"keystone_password": "some-password",
	"delegate": {
    "name": "cni-ovs",
    "type": "ovs",
    "bridge": "br-int"
  },
  "metadata": {
    "app_id": "some-app-guid",
    "org_id": "some-org-guid",
    "policy_group_id": "some-group-policy-id",
    "space_id": "some-space-guid"
  }
}
*/

const defaultStateDir = "/var/lib/cni/gofer"

const defaultCIDR = "10.0.3.0/24"
const defaultNetStart = "10.0.3.20"
const defaultNetEnd = "10.0.3.150"

type NetConf struct {
	types.NetConf
	NeutronURL       string                 `json:"neutron_url"`
	KeystoneURL      string                 `json:"keystone_url"`
	KeystoneUsername string                 `json:"keystone_username"`
	KeystonePassword string                 `json:"keystone_password"`
	StateDir         string                 `json:"state_dir"`
	Delegate         map[string]interface{} `json:"delegate"`
	Metadata         map[string]interface{} `json:"metadata"`
}

type ContainerState struct {
	IP            string `json:"ip"`
	NeutronPortID string `json:"neutron_port_id"`
}

func loadNetConfig(stdin []byte) (*NetConf, error) {
	n := &NetConf{
		StateDir: defaultStateDir,
	}
	if err := json.Unmarshal(stdin, n); err != nil {
		return nil, fmt.Errorf("failed to load netconf: %v", err)
	}

	if n.NeutronURL == "" {
		return nil, errors.New("missing 'neutronURL' in CNI net config")
	}

	if len(n.Delegate) == 0 {
		return nil, errors.New("missing 'delegate' in CNI net config")
	}
	return n, nil
}

func delegateAdd(id string, netconf map[string]interface{}) error {
	netconfBytes, err := json.Marshal(netconf)
	if err != nil {
		return fmt.Errorf("error marshalling delegate netconf: %v", err)
	}

	result, err := invoke.DelegateAdd(netconf["type"].(string), netconfBytes)
	if err != nil {
		return fmt.Errorf("error invoking delegate: %v", err)
	}

	return result.Print()
}

func delegateDel(id string, netconf map[string]interface{}) error {
	netconfBytes, err := json.Marshal(netconf)
	if err != nil {
		return fmt.Errorf("error marshalling delegate netconf: %v", err)
	}

	err = invoke.DelegateDel(netconf["type"].(string), netconfBytes)
	if err != nil {
		return fmt.Errorf("error invoking delegate: %v", err)
	}

	return nil
}

func getMetadata(key string, metadata map[string]interface{}) (string, error) {
	v, ok := metadata[key]
	if !ok {
		return "", fmt.Errorf("missing '%s' in metadata", key)
	}

	value, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("invalid type for '%s' in metadata", key)
	}

	return value, nil
}

func cmdAdd(args *skel.CmdArgs) error {
	n, err := loadNetConfig(args.StdinData)
	if err != nil {
		return err
	}

	networkName, err := getMetadata("space_id", n.Metadata)
	if err != nil {
		// TODO: temp hack to get around staging containers
		networkName = "defaultNetwork"
	}

	keystoneClient, err := keystone.NewClient(n.KeystoneURL)
	if err != nil {
		return err
	}

	// get token for username, password and domain name
	// TODO: inject domain name as well?
	auth := keystone.NewAuth(n.KeystoneUsername, n.KeystonePassword, "Default")
	authToken, err := keystoneClient.Tokens(auth)
	if err != nil {
		return err
	}

	client, err := neutron.NewClient(n.NeutronURL, authToken)
	if err != nil {
		return err
	}

	networks, err := client.NetworksByName(networkName)
	if err != nil {
		return err
	}

	var network neutron.Network

	// if not found try to create net/subnet
	if len(networks) == 0 {
		// create network
		net := neutron.Network{
			Name:         networkName,
			Description:  networkName,
			AdminStateUp: true,
		}
		network, err = client.CreateNetwork(net)
		if err != nil {
			return err
		}

		// create subnet
		subnet := neutron.Subnet{
			NetworkID: network.ID,
			IPVersion: 4,
			CIDR:      defaultCIDR,
			AllocationPools: []neutron.AllocationPool{
				{
					Start: defaultNetStart,
					End:   defaultNetEnd,
				},
			},
		}

		_, err = client.CreateSubnet(subnet)
		if err != nil {
			return err
		}
	} else {
		network = networks[0]
	}

	networkID := network.ID

	// create neutron port
	port := neutron.Port{
		NetworkID:    networkID,
		Name:         args.ContainerID,
		AdminStateUp: true,
	}

	p, err := client.CreatePort(port)
	if err != nil {
		return fmt.Errorf("error calling neutron create port: %v", err)
	}

	if len(p.FixedIPs) != 1 {
		return fmt.Errorf("error neutron create port failed to allocate ip address")
	}

	// pass ip_addr to delegate CNI plugin
	ip := p.FixedIPs[0].IPAddress
	cidr := fmt.Sprintf("%s/32", ip)
	n.Delegate["ip"] = ip
	n.Delegate["cidr"] = cidr

	err = delegateAdd(args.ContainerID, n.Delegate)
	if err != nil {
		// attempt to cleanup / delete port, but preserve original err
		client.DeletePort(port.ID)
		return fmt.Errorf("error calling delegate : %v", err)
	}

	// save container state (container id, ip, neutron port id)
	cs := ContainerState{
		IP:            cidr,
		NeutronPortID: p.ID,
	}
	err = saveContainerState(args.ContainerID, cs, n.StateDir)
	if err != nil {
		return err
	}
	return err
}

func saveContainerState(id string, cs ContainerState, stateDir string) error {
	bytes, err := json.Marshal(cs)
	if err != nil {
		return err
	}
	path := filepath.Join(stateDir, id)
	return ioutil.WriteFile(path, bytes, 0644)
}

func loadContainerState(id, stateDir string) (ContainerState, error) {
	empty := ContainerState{}
	bytes, err := ioutil.ReadFile(filepath.Join(stateDir, id))
	if err != nil {
		return empty, err
	}

	var cs ContainerState
	err = json.Unmarshal(bytes, &cs)
	if err != nil {
		return empty, err
	}
	return cs, nil
}

func removeContainerState(id, stateDir string) error {
	err := os.Remove(filepath.Join(stateDir, id))
	if err != nil {
		return err
	}
	return nil
}

func cmdDel(args *skel.CmdArgs) error {
	n, err := loadNetConfig(args.StdinData)
	if err != nil {
		return err
	}

	// invoke delegate
	err = delegateDel(args.ContainerID, n.Delegate)
	if err != nil {
		return fmt.Errorf("error calling delegate : %v", err)
	}

	keystoneClient, err := keystone.NewClient(n.KeystoneURL)
	if err != nil {
		return err
	}

	// get token for username, password and domain name
	// TODO: inject domain name as well?
	auth := keystone.NewAuth(n.KeystoneUsername, n.KeystonePassword, "Default")
	authToken, err := keystoneClient.Tokens(auth)
	if err != nil {
		return err
	}

	client, err := neutron.NewClient(n.NeutronURL, authToken)
	if err != nil {
		return err
	}

	// load container state (ip, neutron port id)
	cs, err := loadContainerState(args.ContainerID, n.StateDir)
	if err != nil {
		return err
	}

	// delete neutron port
	err = client.DeletePort(cs.NeutronPortID)
	if err != nil {
		return fmt.Errorf("error calling neutron delete port: %v", err)
	}

	// remove container state file
	err = removeContainerState(args.ContainerID, n.StateDir)
	if err != nil {
		return err
	}
	return err
}

func main() {
	skel.PluginMain(cmdAdd, cmdDel, version.Legacy)
}
