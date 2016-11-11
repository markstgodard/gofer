package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/containernetworking/cni/pkg/invoke"
	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/cni/pkg/version"
	"github.com/markstgodard/go-neutron/neutron"
)

/*
{
	"cniVersion": "0.2.0",
  "name": "cni-neutron-ovs",
  "type": "gofer",
	"neutronURL": "https://somehost:9696",
	"delegate": {
    "name": "cni-ovs",
    "type": "ovs",
    "bridge": "br-int"
  },
   "metadata": {
    "app_id": "app guid",
    "space_id": "space guid"
  }
}
*/

type NetConf struct {
	types.NetConf
	NeutronURL string
	Delegate   map[string]interface{} `json:"delegate"`
}

func loadNetConfig(stdin []byte) (*NetConf, error) {
	n := &NetConf{}
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

// parse extra args i.e. AUTH_TOKEN=foo;NETWORK_ID=bar
func parseExtraArgs(args string) (map[string]string, error) {
	m := make(map[string]string)

	items := strings.Split(args, ";")
	for _, item := range items {
		kv := strings.Split(item, "=")
		if len(kv) != 2 {
			return nil, fmt.Errorf("CNI_ARGS invalid key/value pair: %s\n", kv)
		}
		m[kv[0]] = kv[1]
	}
	return m, nil
}

func getExtraArg(key string, extra map[string]string) (string, error) {
	v, ok := extra[key]
	if !ok {
		return "", fmt.Errorf("missing '%s' in CNI_ARGS", key)
	}
	return v, nil
}

func cmdAdd(args *skel.CmdArgs) error {
	n, err := loadNetConfig(args.StdinData)
	if err != nil {
		return err
	}

	extra, err := parseExtraArgs(args.Args)
	if err != nil {
		return err
	}

	networkID, err := getExtraArg("NETWORK_ID", extra)
	if err != nil {
		return err
	}

	authToken, err := getExtraArg("AUTH_TOKEN", extra)
	if err != nil {
		return err
	}

	client, err := neutron.NewClient(n.NeutronURL, authToken)
	if err != nil {
		return err
	}

	// create neutron port
	port := neutron.Port{
		NetworkID:    networkID,
		Name:         args.ContainerID,
		DeviceID:     "d6b4d3a5-c700-476f-b609-1493dd9dadc0",
		AdminStateUp: true,
	}

	p, err := client.CreatePort(port)
	if err != nil {
		return fmt.Errorf("error calling neutron create port: %v", err)
	}

	if len(p.FixedIPs) != 1 {
		return fmt.Errorf("error neutron create port failed to allocate ip address")
	}

	os.Setenv("NEUTRON_IP", p.FixedIPs[0].IPAddress+"/32")

	err = delegateAdd(args.ContainerID, n.Delegate)
	if err != nil {
		return fmt.Errorf("error calling delegate : %v", err)
	}
	return err
}

func cmdDel(args *skel.CmdArgs) error {
	return errors.New("not implemented")
}

func main() {
	skel.PluginMain(cmdAdd, cmdDel, version.Legacy)
}
