package node

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pion/stun/v2"
)

const (
	StunServerAddr = "stun.l.google.com:19302"
)

var (
	ErrBindInactive = errors.New("inactive bind")
)

type UdpMux struct {
	nodeID               uint64
	mu                   sync.Mutex
	conn                 net.PacketConn
	peers                map[uint64]*Peer
	publicAddr           netip.AddrPort
	closed               chan bool
	udpPort              int
	unspecifiedEndpoints []netip.AddrPort
	bindActive           bool
	rc                   *RelayClient
	bindCtx              context.Context
	bindCancel           context.CancelFunc
}

type UdpMuxOpts struct {
	NodeID      uint64
	Port        int
	RelayClient *RelayClient
}

func NewUdpMux(opts *UdpMuxOpts) *UdpMux {
	return &UdpMux{
		peers:   make(map[uint64]*Peer),
		closed:  make(chan bool),
		udpPort: opts.Port,
		nodeID:  opts.NodeID,
		rc:      opts.RelayClient,
	}
}

func (m *UdpMux) RegisterPeer(peer *Peer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.peers[peer.id] = peer
	return nil
}

func (m *UdpMux) DeregisterPeer(peerID uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.peers, peerID)
	return nil
}

func (m *UdpMux) GetEndpoints() []netip.AddrPort {
	m.mu.Lock()
	defer m.mu.Unlock()
	var eps []netip.AddrPort
	eps = append(eps, m.unspecifiedEndpoints...)
	if m.publicAddr.Addr().IsValid() {
		eps = append(eps, m.publicAddr)
	}
	return eps
}

func (mux *UdpMux) doStun() {
	if mux.IsClosed() {
		return
	}

	stunAddr, err := net.ResolveUDPAddr("udp4", StunServerAddr)
	if err != nil {
		log.Printf("error sending stun request")
		return
	}

	msg := stun.MustBuild(stun.TransactionID, stun.BindingRequest)
	_, err = mux.conn.WriteTo(msg.Raw, stunAddr)
	if err != nil {
		log.Printf("error sending stun request")
		return
	}
}

func (m *UdpMux) Bind() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.conn != nil {
		return errors.New("bind error: unbind must be called before re-binding")
	}

	var err error
	m.conn, err = net.ListenPacket("udp4", fmt.Sprintf("0.0.0.0:%d", m.udpPort))
	if err != nil {
		return fmt.Errorf("bind error: %s", err)
	}

	if m.udpPort == 0 {
		portStr := strings.Split(m.conn.LocalAddr().String(), ":")
		port, _ := strconv.Atoi(portStr[1])
		// Since we don't specify a listen address, we need to grab the addresses ourselves
		unspecified, err := getEndpointsForUnspecified(uint16(port))
		if err != nil {
			log.Printf("error getting listen addresses for unspecified: %s", err)
		}
		m.unspecifiedEndpoints = unspecified
	} else {
		m.unspecifiedEndpoints = append(m.unspecifiedEndpoints, netip.MustParseAddrPort(m.conn.LocalAddr().String()))
	}

	m.bindCtx, m.bindCancel = context.WithCancel(context.Background())

	dialCtx, _ := context.WithTimeout(m.bindCtx, time.Second*5)
	err = m.rc.Connect(dialCtx)
	if err != nil {
		m.bindCancel()
		log.Printf("error connecting to relay - cancelling bind: %s", err)
		m.conn.Close()
		m.conn = nil
		m.bindActive = false
	}

	m.bindActive = true
	go m.startReadLoop()
	// m.startStunDiscovery()
	go m.relayReadLoop()

	return nil
}

func (m *UdpMux) Unbind() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.bindActive {
		return errors.New("mux does not have an active bind. call bind first")
	}

	// trigger first before closing conn to signal read loop to stop without error
	m.bindActive = false
	m.bindCancel()
	conn := m.conn
	m.conn = nil

	if err := conn.Close(); err != nil {
		return err
	}

	if err := m.rc.Close(); err != nil {
		return err
	}

	return nil
}

func (m *UdpMux) startReadLoop() {
	buf := make([]byte, 1500)
	for {

		n, addr, err := m.read(buf)
		if err != nil {
			if err == ErrBindInactive {
				return
			}
			if errors.Is(err, net.ErrClosed) {
				return
			}
			log.Printf("error reading from udp conn: %s", err)
			return
		}

		// See if it is a stun packet
		if stun.IsMessage(buf[:n]) {
			msg := &stun.Message{
				Raw: append([]byte{}, buf[:n]...),
			}
			m.handleStun(msg)
			continue
		}

		// Validate its not a packet thats too small
		if n < 8 {
			log.Println("packet too small - dropping")
			continue
		}

		// Not a stun message, check for a data packet
		// First 8 bytes should be the peer ID
		peerID := binary.BigEndian.Uint64(buf[:8])
		if peerID == 0 {
			log.Println("invalid peer ID - dropping")
			continue
		}

		m.mu.Lock()
		peer, ok := m.peers[peerID]
		m.mu.Unlock()
		if !ok {
			log.Printf("peer id %d not found - dropping", peerID)
			continue
		}

		packet := make([]byte, n-8)
		copy(packet, buf[8:])
		peer.inboundPacket(packet, addr)
	}
}

func (m *UdpMux) Write(packet []byte, addr netip.AddrPort) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.bindActive {
		return ErrBindInactive
	}
	header := binary.BigEndian.AppendUint64([]byte{}, m.nodeID)
	header = append(header, packet...)
	_, err := m.conn.(*net.UDPConn).WriteToUDPAddrPort(header, addr)
	if err != nil {
		return err
	}
	return nil
}

func (m *UdpMux) WriteRelayPacket(packet []byte, dstNodeID uint64) error {
	if m.rc == nil {
		return nil
	}
	if !m.bindActive {
		return ErrBindInactive
	}

	err := m.rc.SendPacket(m.bindCtx, dstNodeID, packet)
	return err
}

func (m *UdpMux) relayReadLoop() {
	if m.rc == nil {
		return
	}

	for {
		if m.IsClosed() {
			return
		}

		m.mu.Lock()
		bindActive := m.bindActive
		m.mu.Unlock()

		if !bindActive {
			return
		}

		peerID, packet, err := m.rc.ReadPacket(m.bindCtx)
		if err != nil {
			log.Printf("error reading relay packets: %s", err)
			return
		}

		m.mu.Lock()
		peer, ok := m.peers[peerID]
		m.mu.Unlock()
		if !ok {
			log.Printf("peer id %d not found for relay packet", peerID)
			continue
		}

		peer.inboundRelayPacket(packet)
	}
}

func (m *UdpMux) handleStun(msg *stun.Message) {
	err := msg.Decode()
	if err != nil {
		log.Printf("error decoding stun message: %v", err)
		return
	}

	if msg.Type != stun.BindingSuccess {
		log.Printf("invalid stun response type: %s", msg.Type.String())
		return
	}

	var xor stun.XORMappedAddress
	err = xor.GetFrom(msg)
	if err != nil {
		log.Printf("error getting xormappedaddr from msg: %v", err)
		return
	}

	stunAddr, err := netip.ParseAddrPort(xor.String())
	if err != nil {
		log.Printf("error parsing stun xor-mapped address: %v", err)
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.publicAddr = stunAddr
}

func (m *UdpMux) read(buf []byte) (n int, addr netip.AddrPort, err error) {
	// m.mu.Lock()
	// defer m.mu.Unlock()
	udpConn := m.conn.(*net.UDPConn)
	n, addr, err = udpConn.ReadFromUDPAddrPort(buf)
	if err != nil {
		if !m.bindActive {
			err = ErrBindInactive
		}
		if errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) {
			err = net.ErrClosed
		}
		return
	}
	return
}

func (m *UdpMux) IsClosed() bool {
	select {
	case <-m.closed:
		return true
	default:
		return false
	}
}

func getEndpointsForUnspecified(port uint16) ([]netip.AddrPort, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	var eps []netip.AddrPort
	parse := func(ip net.IP) netip.AddrPort {
		a, ok := netip.AddrFromSlice(ip)
		if !ok {
			return netip.AddrPort{}
		}
		a = a.Unmap()
		return netip.AddrPortFrom(a, port)
	}

	for _, i := range ifaces {
		if i.Flags&net.FlagUp == 0 {
			continue
		}
		if i.Flags&net.FlagLoopback != 0 {
			continue
		}
		if strings.Contains(i.Name, "tun") {
			continue
		}

		addrs, err := i.Addrs()
		if err != nil {
			continue
		}

		for _, a := range addrs {
			switch v := a.(type) {
			case *net.UDPAddr:
				// fmt.Printf("udp %s\n", v.String())

				if v.IP.To4() != nil {
					eps = append(eps, parse(v.IP))
				}
			case *net.IPAddr:
				// fmt.Printf("ipaddr %v : %s (%s)\n", i.Name, v, v.IP.DefaultMask())
				if v.IP.To4() != nil {
					eps = append(eps, parse(v.IP))
				}
			case *net.IPNet:
				// fmt.Printf("net %v : %s [%v/%v]\n", i.Name, v, v.IP, v.Mask)
				if v.IP.To4() != nil {
					eps = append(eps, parse(v.IP))
				}
			default:
				fmt.Printf("unhandled address type %T\n", v.String())
			}
		}
	}
	return eps, nil
}
