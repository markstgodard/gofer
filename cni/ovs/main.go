package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
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
	BrName  string `json:"bridge"`
	MTU     int    `json:"mtu"`
	BinPath string `json:"bin_path"`
}

func init() {
	// this ensures that main runs only on main thread (thread group leader).
	// since namespace ops (unshare, setns) are done for a single thread, we
	// must ensure that the goroutine does not jump from OS thread to thread
	runtime.LockOSThread()
}

func loadNetConf(bytes []byte) (*NetConf, error) {
	n := &NetConf{
		BrName:  defaultBrName,
		BinPath: "/var/vcap/packages/openvswitch/bin",
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

	hostIfName, err := setupVeth(netns, args.IfName, n.MTU)
	if err != nil {
		return err
	}

	// tunnelPort     = 10
	// tunnelID       = 101
	// bridgeName     = "ovs-bridge"
	// tunnelPortName = "remote-tun"

	// TODO: hack
	containerIP := ip
	containerMAC := "00:00:00:00:00:01"
	tunnelID := 101
	ovsPortNumber := 10

	err = connectToOVS(n.BinPath, n.BrName, hostIfName, ovsPortNumber, containerIP, containerMAC, tunnelID)
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

var debug bool

func execCommand(command string, args ...string) ([]byte, error) {
	if debug {
		fmt.Printf("%s", command)
		for _, arg := range args {
			fmt.Printf(" %s", arg)
		}
		fmt.Printf("\n")
	}
	return exec.Command(command, args...).CombinedOutput()
}

func addFlow(path, containerIP, containerMAC, bridgeName string, tunnelPort, tunnelID int) error {
	addMacFlow := fmt.Sprintf("%s/ovs-ofctl add-flow %s table=1,tun_id=%d,dl_dst=%s,actions=output:%d", path, bridgeName, tunnelID, containerMAC, tunnelPort)
	output, err := execCommand("bash", "-c", addMacFlow)
	if err != nil {
		return fmt.Errorf("%s: %s", err, output)
	}

	addIPFlow := fmt.Sprintf("%s/ovs-ofctl add-flow %s table=1,tun_id=%d,arp,nw_dst=%s,actions=output:%d", path, bridgeName, tunnelID, containerIP, tunnelPort)
	output, err = execCommand("bash", "-c", addIPFlow)
	if err != nil {
		return fmt.Errorf("%s: %s", err, output)
	}

	return nil
}

func connectToOVS(path, ovsBridgeName, interfaceName string, ovsPortNumber int, containerIP, containerMAC string, tunnelID int) error {
	cmd := fmt.Sprintf("%s/ovs-vsctl add-port %s %s -- set interface %s ofport_request=%d", path, ovsBridgeName, interfaceName, interfaceName, ovsPortNumber)
	output, err := execCommand("bash", "-c", cmd)
	if err != nil {
		return fmt.Errorf("%s: %s", err, output)
	}

	err = addFlow(path, containerIP, containerMAC, ovsBridgeName, ovsPortNumber, tunnelID)
	if err != nil {
		panic(err)
	}

	anotherFlow := fmt.Sprintf("%s/ovs-ofctl add-flow %s 'table=0,in_port=%d,actions=set_field:%d->tun_id,resubmit(,1)'", path, ovsBridgeName, ovsPortNumber, tunnelID)
	output, err = execCommand("bash", "-c", anotherFlow)
	if err != nil {
		return fmt.Errorf("%s: %s", err, output)
	}

	ifUpCommand := fmt.Sprintf("ifconfig %s up", interfaceName)
	output, err = execCommand("bash", "-c", ifUpCommand)
	if err != nil {
		return fmt.Errorf("%s: %s", err, output)
	}

	return nil
}

func cmdDel(args *skel.CmdArgs) error {
	return errors.New("not implemented")
}

func main() {
	skel.PluginMain(cmdAdd, cmdDel, version.Legacy)
}
