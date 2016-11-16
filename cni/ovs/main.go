package main

import (
	"errors"
	"fmt"
	"net"
	"os"

	"github.com/containernetworking/cni/pkg/ip"
	"github.com/containernetworking/cni/pkg/ns"
	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/cni/pkg/version"
)

func cmdAdd(args *skel.CmdArgs) error {
	ip := os.Getenv("NEUTRON_IP")

	n, err := loadNetConf(args.StdinData)
	if err != nil {
		return err
	}

	netns, err := ns.GetNS(args.Netns)
	if err != nil {
		return fmt.Errorf("failed to open netns %q: %v", args.Netns, err)
	}
	defer netns.Close()

	hostIfName := fmt.Sprintf("o-%s-0", args.ContainerID)
	containerIfName := fmt.Sprintf("o-%s-1", args.ContainerID)

	hostIfName, err = setupVeth(netns, args.IfName, n.MTU)
	if err != nil {
		return err
	}

	err = connectToOVS(ovsBridgeName, hostIfName, ovsPortNumber, containerIP, containerMAC, tunnelID)
	if err != nil {
		return err
	}

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

func setupVeth(netns ns.NetNS, ifName string, mtu int) (string, error) {
	var hostVethName string

	err := netns.Do(func(hostNS ns.NetNS) error {
		// create the veth pair in the container and move host end into host netns
		hostVeth, _, err := ip.SetupVeth(ifName, mtu, hostNS)
		if err != nil {
			return "", err
		}

		hostVethName = hostVeth.Attrs().Name
		return nil
	})
	if err != nil {
		return "", err
	}

	// need to lookup hostVeth again as its index has changed during ns move
	hostVeth, err := netlink.LinkByName(hostVethName)
	if err != nil {
		return "", fmt.Errorf("failed to lookup %q: %v", hostVethName, err)
	}

	// connect host veth end to the bridge
	// if err = netlink.LinkSetMaster(hostVeth, br); err != nil {
	// 	return fmt.Errorf("failed to connect %q to bridge %v: %v", hostVethName, br.Attrs().Name, err)
	// }

	return nil
}

func cmdDel(args *skel.CmdArgs) error {
	return errors.New("not implemented")
}

func main() {
	skel.PluginMain(cmdAdd, cmdDel, version.Legacy)
}
