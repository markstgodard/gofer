package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"

	"github.com/containernetworking/cni/pkg/ip"
	"github.com/containernetworking/cni/pkg/ns"
	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/cni/pkg/version"
	"github.com/vishvananda/netlink"
)

const defaultBrName = "ovs-bridge"

type NetConf struct {
	types.NetConf
	BrName string `json:"bridge"`
	MTU    int    `json:"mtu"`
}

func init() {
	// this ensures that main runs only on main thread (thread group leader).
	// since namespace ops (unshare, setns) are done for a single thread, we
	// must ensure that the goroutine does not jump from OS thread to thread
	runtime.LockOSThread()
}

func loadNetConf(bytes []byte) (*NetConf, error) {
	n := &NetConf{
		BrName: defaultBrName,
	}
	if err := json.Unmarshal(bytes, n); err != nil {
		return nil, fmt.Errorf("failed to load netconf: %v", err)
	}
	return n, nil
}

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

	// hostIfName := fmt.Sprintf("o-%s-0", args.ContainerID)
	// containerIfName := fmt.Sprintf("o-%s-1", args.ContainerID)

	// hostIfName, err = setupVeth(netns, args.IfName, n.MTU)
	_, err = setupVeth(netns, args.IfName, n.MTU)
	if err != nil {
		return err
	}

	// err = connectToOVS(ovsBridgeName, hostIfName, ovsPortNumber, containerIP, containerMAC, tunnelID)
	// if err != nil {
	// 	return err
	// }

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
			return err
		}

		hostVethName = hostVeth.Attrs().Name
		return nil
	})
	if err != nil {
		return "", err
	}

	// need to lookup hostVeth again as its index has changed during ns move
	// hostVeth, err := netlink.LinkByName(hostVethName)
	_, err = netlink.LinkByName(hostVethName)
	if err != nil {
		return "", fmt.Errorf("failed to lookup %q: %v", hostVethName, err)
	}

	// connect host veth end to the bridge
	// if err = netlink.LinkSetMaster(hostVeth, br); err != nil {
	// 	return fmt.Errorf("failed to connect %q to bridge %v: %v", hostVethName, br.Attrs().Name, err)
	// }

	// return hostVeth
	return hostVethName, nil
}

func cmdDel(args *skel.CmdArgs) error {
	return errors.New("not implemented")
}

func main() {
	skel.PluginMain(cmdAdd, cmdDel, version.Legacy)
}
