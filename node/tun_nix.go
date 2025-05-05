package node

import (
	"fmt"
	"log"
	"net/netip"
	"os/exec"
	"runtime"

	"github.com/songgao/water"
)

// Currently, this is used for Mac/Linux Tunnels
type NixTun struct {
	ifce *water.Interface
}

func NewTun() (Tun, error) {
	ifce, err := water.New(water.Config{DeviceType: water.TUN})
	if err != nil {
		return nil, err
	}

	return &NixTun{ifce: ifce}, nil
}

func (n *NixTun) Read(b []byte) (int, error) {
	return n.ifce.Read(b)
}

func (n *NixTun) Write(b []byte) (int, error) {
	return n.ifce.Write(b)
}

func (n *NixTun) Name() string {
	return n.ifce.Name()
}

func (n *NixTun) Close() error {
	return n.ifce.Close()
}

func (n *NixTun) MTU() (int, error) {
	return MTU, nil
}

func (n *NixTun) ConfigureIPAddress(addr netip.Addr, prefix netip.Prefix) error {
	switch runtime.GOOS {
	case "linux":
		if err := exec.Command("/sbin/ip", "link", "set", "dev", n.Name(), "mtu", "1400").Run(); err != nil {
			return fmt.Errorf("ip link error: %w", err)
		}
		if err := exec.Command("/sbin/ip", "addr", "add", addr.String()+"/32", "dev", n.Name()).Run(); err != nil {
			return fmt.Errorf("ip addr error: %w", err)
		}
		if err := exec.Command("/sbin/ip", "link", "set", "dev", n.Name(), "up").Run(); err != nil {
			return fmt.Errorf("ip link error: %w", err)
		}
		if err := exec.Command("/sbin/ip", "route", "add", prefix.String(), "via", addr.String()).Run(); err != nil {
			log.Fatalf("route add error: %v", err)
		}
	case "darwin":
		if err := exec.Command("/sbin/ifconfig", n.Name(), "mtu", "1400", addr.String(), addr.String(), "up").Run(); err != nil {
			return fmt.Errorf("ifconfig error %v: %w", n.Name(), err)
		}
		if err := exec.Command("/sbin/route", "-n", "add", "-net", prefix.String(), addr.String()).Run(); err != nil {
			log.Fatalf("route add error: %v", err)
		}
	default:
		return fmt.Errorf("no tun support for: %v", runtime.GOOS)
	}

	log.Printf("set tunnel IP successful: %v %v", n.Name(), addr.String())
	log.Printf("set route successful: %v via %v dev %v", prefix.String(), addr.String(), n.Name())
	return nil
}
