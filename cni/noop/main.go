package main

import (
	"net"
	"os"

	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/cni/pkg/version"
)

func cmdAdd(args *skel.CmdArgs) error {
	ip := os.Getenv("NEUTRON_IP")

	result := types.Result{}
	if ip != "" {
		_, ipn, err := net.ParseCIDR(ip)
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
