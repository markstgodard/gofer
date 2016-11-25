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
const defaultOvsBinPath = "/var/vcap/packages/openvswitch/bin"

type NetConf struct {
	types.NetConf
	BrName  string `json:"bridge"`
	MTU     int    `json:"mtu"`
	BinPath string `json:"bin_path"`
	IP      string `json:"ip"`
	CIDR    string `json:"cidr"`
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
		BinPath: defaultOvsBinPath,
	}
	if err := json.Unmarshal(bytes, n); err != nil {
		return nil, fmt.Errorf("failed to load netconf: %v", err)
	}
	return n, nil
}

func cmdAdd(args *skel.CmdArgs) error {
	n, err := loadNetConf(args.StdinData)
	if err != nil {
		return err
	}

	if n.IP == "" {
		return errors.New("Missing 'ip' in delegate call to CNI plugin!")
	}

	if n.CIDR == "" {
		return errors.New("Missing 'cidr' in delegate call to CNI plugin!")
	}

	netns, err := ns.GetNS(args.Netns)
	if err != nil {
		return fmt.Errorf("failed to open netns %q: %v", args.Netns, err)
	}
	defer netns.Close()

	vr, err := setupVeth(netns, args.IfName, n.MTU, n.IP, n.CIDR)
	if err != nil {
		return err
	}

	// tunnelPort     = 10
	// tunnelID       = 101
	// bridgeName     = "ovs-bridge"
	// tunnelPortName = "remote-tun"

	containerIP := n.IP
	containerMAC := vr.HwAddr
	// containerMAC := "00:00:00:00:00:01"
	if vr.HwAddr == "" {
		return fmt.Errorf("Invalid MAC address for container: [%s]", vr.HwAddr)
	}

	// TODO: hack for now
	tunnelID := 101
	ovsPortNumber := 10

	err = connectToOVS(n.BinPath, n.BrName, vr.HostIfName, ovsPortNumber, containerIP, containerMAC, tunnelID)
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
			Routes:  vr.Routes,
			Gateway: vr.GW,
		}
	}

	return result.Print()
}

type vethResult struct {
	HostIfName string
	HwAddr     string
	GW         net.IP
	Routes     []types.Route
}

func setupVeth(netns ns.NetNS, ifName string, mtu int, ipAddr, cidr string) (vethResult, error) {
	var result vethResult
	var routes []types.Route

	err := netns.Do(func(hostNS ns.NetNS) error {
		// create the veth pair in the container and move host end into host netns
		hostVeth, _, err := ip.SetupVeth(ifName, mtu, hostNS)
		if err != nil {
			return err
		}
		result.HostIfName = hostVeth.Attrs().Name

		// set HW addr
		ip4 := net.ParseIP(ipAddr)
		if err := ip.SetHWAddrByIP(ifName, ip4, nil); err != nil {
			return err
		}

		nl, err := netlink.LinkByName(ifName)
		if err != nil {
			return err
		}

		addr, err := netlink.ParseAddr(cidr)
		if err != nil {
			return err
		}

		// fmt.Sprintf("ip addr add %s/%d dev %s", containerIP, containerIPAddressMask, containerIfName))
		err = netlink.AddrAdd(nl, addr)
		if err != nil {
			return err
		}

		result.HwAddr = nl.Attrs().HardwareAddr.String()

		if err = netlink.LinkSetUp(nl); err != nil {
			return fmt.Errorf("failed to set %q UP: %v", ifName, err)
		}

		// TODO: ultra hack
		_, ipn, err := net.ParseCIDR("10.0.3.0/24")
		if err != nil {
			return err
		}
		routes = append(routes, types.Route{
			Dst: net.IPNet{
				IP:   ipn.IP,
				Mask: ipn.Mask,
			},
		})

		// _, ipn, err = net.ParseCIDR("0.0.0.0/0")
		// if err != nil {
		// 	return err
		// }
		// gwAddr := net.ParseIP("10.0.3.1")
		// result.GW = gwAddr
		// routes = append(routes, types.Route{
		// 	Dst: net.IPNet{
		// 		IP:   ipn.IP,
		// 		Mask: ipn.Mask,
		// 	},
		// 	GW: gwAddr,
		// })

		// "result":
		//    IP4:{IP:{IP:10.255.47.89 Mask:ffffff00} Gateway:10.255.47.1
		// Routes:[
		//    {Dst:{IP:10.255.0.0 Mask:ffff0000} GW:\\u003cnil\\u003e}
		//    {Dst:{IP:0.0.0.0 Mask:00000000} GW:10.255.47.1}]},
		//    DNS:{Nameservers:[] Domain: Search:[] Options:[]} }

		// default via 10.255.47.1 dev eth0
		// 10.255.0.0/16 via 10.255.47.1 dev eth0
		// 10.255.47.0/24 dev eth0  proto kernel  scope link  src 10.255.47.71

		for _, r := range routes {
			gw := r.GW
			// if gw == nil {
			// 	gw = result.GW
			// }
			if err = ip.AddRoute(&r.Dst, gw, nl); err != nil {
				if !os.IsExist(err) {
					return fmt.Errorf("failed to add route '%v via %v dev %v': %v", r.Dst, gw, ifName, err)
				}
			}
		}

		return nil
	})
	if err != nil {
		return result, err
	}

	_, err = netlink.LinkByName(result.HostIfName)
	if err != nil {
		return result, fmt.Errorf("failed to lookup %q: %v", result.HostIfName, err)
	}

	return result, nil
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

func connectToOVS(path, ovsBridgeName, interfaceName string, ovsPortNumber int, containerIP, containerMAC string, tunnelID int) error {
	cmd := fmt.Sprintf("%s/ovs-vsctl add-port %s %s -- set interface %s ofport_request=%d", path, ovsBridgeName, interfaceName, interfaceName, ovsPortNumber)
	output, err := execCommand("bash", "-c", cmd)
	if err != nil {
		return fmt.Errorf("%s: %s", err, output)
	}

	err = addFlow(path, containerIP, containerMAC, ovsBridgeName, ovsPortNumber, tunnelID)
	if err != nil {
		return fmt.Errorf("error adding flow using ip [%s] mac [%s] port [%d] tun [%d] error: %s\n", containerIP, containerMAC, ovsPortNumber, tunnelID, err)
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

func removeFromOVS(path, ovsBridgeName, interfaceName string) error {
	cmd := fmt.Sprintf("%s/ovs-vsctl del-port %s %s", path, ovsBridgeName, interfaceName)
	output, err := execCommand("bash", "-c", cmd)
	if err != nil {
		return fmt.Errorf("%s: %s", err, output)
	}

	// TODO: delete flows?

	return nil
}

func cmdDel(args *skel.CmdArgs) error {
	// n, err := loadNetConf(args.StdinData)
	// if err != nil {
	// 	return err
	// }

	if args.Netns == "" {
		return nil
	}

	// err = removeFromOVS(n.BinPath, n.BrName, hostIfName)
	// if err != nil {
	// 	return err
	// }

	// var ipn *net.IPNet
	// err := ns.WithNetNSPath(args.Netns, func(_ ns.NetNS) error {
	// 	var err error
	// 	ipn, err = ip.DelLinkByNameAddr(args.IfName, netlink.FAMILY_V4)
	// 	return err
	// })

	// if err != nil {
	// 	return err
	// }
	return nil
}

func main() {
	skel.PluginMain(cmdAdd, cmdDel, version.Legacy)
}
