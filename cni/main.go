package main

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/containernetworking/cni/pkg/invoke"
	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/cni/pkg/version"
)

/*
{
  "name": "cni-neutron-ovs",
  "type": "gofer",
	"cniVersion": "0.2.0",
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
	Delegate map[string]interface{} `json:"delegate"`
}

func loadNetConfig(stdin []byte) (*NetConf, error) {
	n := &NetConf{}
	if err := json.Unmarshal(stdin, n); err != nil {
		return nil, fmt.Errorf("failed to load netconf: %v", err)
	}
	return n, nil
}

func delegateAdd(id string, netconf map[string]interface{}) error {
	netconfBytes, err := json.Marshal(netconf)
	if err != nil {
		return fmt.Errorf("error marshalling delegaet netconf: %v", err)
	}

	result, err := invoke.DelegateAdd(netconf["type"].(string), netconfBytes)
	if err != nil {
		return err
	}

	return result.Print()
}

func cmdAdd(args *skel.CmdArgs) error {
	n, err := loadNetConfig(args.StdinData)
	if err != nil {
		return err
	}

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
