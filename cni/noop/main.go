package main

import (
	"encoding/json"
	"fmt"
	"net"

	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/cni/pkg/version"
)

type NetConf struct {
	types.NetConf
	Bridge string `json:"bridge"`
	IP     string `json:"ip"`
	CIDR   string `json:"cidr"`
}

func loadNetConfig(stdin []byte) (*NetConf, error) {
	n := &NetConf{}
	if err := json.Unmarshal(stdin, n); err != nil {
		return nil, fmt.Errorf("failed to load netconf: %v", err)
	}
	return n, nil
}

func cmdAdd(args *skel.CmdArgs) error {
	n, err := loadNetConfig(args.StdinData)
	if err != nil {
		return err
	}

	result := types.Result{}
	if n.CIDR != "" {
		_, ipn, err := net.ParseCIDR(n.CIDR)
		if err != nil {
			return err
		}
		result.IP4 = &types.IPConfig{
			IP: net.IPNet{
				IP:   ipn.IP,
				Mask: ipn.Mask,
			},
		}
	}

	return result.Print()
}

func cmdDel(args *skel.CmdArgs) error {
	return nil
}

func main() {
	skel.PluginMain(cmdAdd, cmdDel, version.Legacy)
}
