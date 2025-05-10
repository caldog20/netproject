package node

import (
	"encoding/binary"
	"encoding/json"
	"log"
	"net/netip"
	"slices"
	"sync"
	"time"
)

const (
	// Magic for ping/pong/endpoints packets
	// otherwise its a data packet through relay
	Magic             uint32 = 0xdeadbeef
	Ping              uint8  = 0x1
	Pong              uint8  = 0x2
	EndpointsRequest  uint8  = 0x3
	EndpointsResponse uint8  = 0x4
	PunchRequest      uint8  = 0x5
	//
	TestDataPacketMagic uint64 = 0xbeefdeadf00dd00f
)

type Endpoints struct {
	Endpoints []netip.AddrPort `json:"endpoints"`
}

type candidate struct {
	endpoint     netip.AddrPort
	lastPing     time.Time
	receivedPong time.Time // zero means no received pong
	rtt          int
	attempts     int
}

type Peer struct {
	id  uint64
	ip  netip.Addr
	mux *UdpMux
	tun Tun
	// peerPublicKey keys.PublicKey
	// nodePublic keys.PublicKey
	// nodePrivate keys.PrivateKey
	mu                 sync.Mutex
	activeAddr         netip.AddrPort
	activeAddrRtt      int
	lastActiveAddrPong time.Time
	candidates         map[netip.AddrPort]*candidate
	lastFullCheck      time.Time
	lastActiveCheck    time.Time
	lastEndpointSync   time.Time
}

func NewPeer(id uint64, mux *UdpMux) *Peer {
	return &Peer{
		id:         id,
		mux:        mux,
		candidates: make(map[netip.AddrPort]*candidate),
	}
}

func (p *Peer) inboundPacket(b []byte, addr netip.AddrPort) {
	if p.isMagicTestPacket(b) {
		log.Println("received magic test packet from udp - sending back")
		time.Sleep(time.Millisecond * 100)
		p.write(b)
		return
	}
	if p.isDiscoveryPacket(b) {
		msgtype := p.decodeHeader(b)
		switch msgtype {
		case Ping:
			p.handlePing(addr)
		case Pong:
			p.handlePong(addr)
		default:
			log.Printf("unknown discovery packet from udp: %d", msgtype)
		}
	}
}

func (p *Peer) inboundRelayPacket(b []byte) {
	if p.isDiscoveryPacket(b) {
		msgtype := p.decodeHeader(b)
		switch msgtype {
		case EndpointsRequest:
			log.Println("got endpoint request")
			p.handleEndpointRequest(b)
		case EndpointsResponse:
			log.Println("got endpoint response")
			p.handleEndpointResponse(b)
		default:
			log.Printf("unknown discovery packet msgtype: %d", msgtype)
			return
		}
		return
	}

	if p.isMagicTestPacket(b) {
		log.Println("received magic test packet from relay - sending back")
		time.Sleep(time.Millisecond * 500)
		p.write(b)
		return
	}
}

func (p *Peer) inboundTunPacket(b []byte) {
	p.write(b)
}

func (p *Peer) sendMagicTestPacket() {
	packet := binary.BigEndian.AppendUint64([]byte{}, TestDataPacketMagic)
	p.write(packet)
}

func (p *Peer) write(b []byte) {
	// We need to send a data packet to the peer
	// Check and see if we have an active address
	// If we do, check how long ago we checked liveliness
	// If we don't, initiaite a full ping to try to find a path
	// We will basically send a message to the other side with our addresses asking for a ping
	// we will then get a message back from the other side with a list of their addresses asking us for a ping
	// We can use the relay while we are waiting for a path
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.activeAddr.IsValid() {
		log.Println("invalid active address")
		if len(p.candidates) == 0 || time.Since(p.lastEndpointSync).Minutes() > 5 {
			p.sendEndpointRequestLocked()
		} else if time.Since(p.lastFullCheck).Seconds() > 10 {
			p.pingAllLocked(false)
		}
		if err := p.mux.WriteRelayPacket(b, p.id); err != nil {
			log.Printf("error writing to relay: %s", err)
		}
	} else {
		if time.Since(p.lastFullCheck).Seconds() > 20 {
			p.pingAllLocked(false)
		} else if time.Since(p.lastActiveCheck).Seconds() >= 9 {
			p.pingEndpointLocked(p.activeAddr)
			p.lastActiveCheck = time.Now()
		} else {
			if time.Since(p.lastActiveAddrPong).Seconds() > 10 {
				// Consider it no long valid
				log.Println("active address no longer valid - using relay")
				p.activeAddr = netip.AddrPort{}
				p.activeAddrRtt = 0
				p.mux.WriteRelayPacket(b, p.id)
				return
			}

		}
		p.mux.Write(b, p.activeAddr)
	}
}

func (p *Peer) isMagicTestPacket(b []byte) bool {
	if len(b) < 8 {
		return false
	}
	if val := binary.BigEndian.Uint64(b[:8]); val == TestDataPacketMagic {
		return true
	}
	return false
}

func (p *Peer) isDiscoveryPacket(b []byte) bool {
	if len(b) < 4 {
		return false
	}
	if val := binary.BigEndian.Uint32(b[:4]); val == Magic {
		return true
	}
	return false
}

func (p *Peer) handlePing(ep netip.AddrPort) {
	log.Printf("got ping from %s", ep.String())
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.candidates[ep]
	if !ok {
		log.Printf("received ping from unknown candidate: %s", ep)
		return
	}
	p.replyToPingLocked(ep)
}

func (p *Peer) replyToPingLocked(ep netip.AddrPort) {
	header := p.encodeHeader(Pong)
	p.mux.Write(header, ep)
}

func (p *Peer) handlePong(ep netip.AddrPort) {
	log.Printf("got pong from %s", ep.String())
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.candidates[ep]
	if !ok {
		log.Printf("received pong for unknown candidate: %s", ep)
		return
	}
	isFirstReply := c.receivedPong.IsZero()
	c.receivedPong = time.Now()
	c.rtt = int(time.Since(c.lastPing))

	if isFirstReply {
		p.pingEndpointLocked(c.endpoint)
		c.lastPing = time.Now()
		return
	}
	// re-evaluate active address
	if !p.activeAddr.IsValid() {
		log.Printf("got an active address %s with rtt: %d", ep, time.Since(c.lastPing).Milliseconds())
		p.activeAddr = ep
		p.activeAddrRtt = c.rtt
	} else {
		if ep != p.activeAddr {
			if c.rtt < p.activeAddrRtt && p.activeAddrRtt != 0 {
				log.Printf("got a better active address %s with rtt: %d", ep, time.Since(c.lastPing).Milliseconds())
				p.activeAddr = ep
				p.activeAddrRtt = c.rtt
				p.lastActiveCheck = time.Now()
				p.lastActiveAddrPong = time.Now()
			}
		} else {
			p.activeAddrRtt = int(time.Since(p.lastActiveCheck))
			p.lastActiveAddrPong = time.Now()
		}
	}
}

func (p *Peer) sendEndpointRequestLocked() {
	p.lastEndpointSync = time.Now()
	p.sendEndpoints(EndpointsRequest)
}

func (p *Peer) handleEndpointRequest(b []byte) {
	eps := &Endpoints{}
	json.Unmarshal(b[5:], eps)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastEndpointSync = time.Now()
	p.addCandidatesLocked(eps.Endpoints)
	// p.pingAllLocked(true)

	p.sendEndpoints(EndpointsResponse)
}

func (p *Peer) handleEndpointResponse(b []byte) {
	eps := &Endpoints{}
	json.Unmarshal(b[5:], eps)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastEndpointSync = time.Now()
	p.addCandidatesLocked(eps.Endpoints)
}

func (p *Peer) maybePruneCandidate(c *candidate) bool {
	if time.Since(c.lastPing).Seconds() <=5 {
		if c.receivedPong.IsZero() || time.Since(c.receivedPong).Seconds() > 20 {
			if c.attempts >=3 {
				return true
			}
		}
	}
	return false
}

func (p *Peer) pingAllWithPunchLocked() {
	p.pingAllLocked(true)
	// p.sendPunchRequest()
}

func (p *Peer) pingAllLocked(force bool) {
	p.lastFullCheck = time.Now()
	log.Printf("doing full ping")
	var pruneList []netip.AddrPort
	for ep, c := range p.candidates {
		if p.maybePruneCandidate(c) {
			pruneList = append(pruneList, ep)
			continue
		}
		if time.Since(c.lastPing).Seconds() >= 5 || force {
			log.Printf("pinging endpoint %s", ep.String())
			c.lastPing = time.Now()
			c.attempts++
			p.pingEndpointLocked(ep)
		}
	}
}

func (p *Peer) pingEndpointLocked(ep netip.AddrPort) {
	header := p.encodeHeader(Ping)
	p.mux.Write(header, ep)
}

func (p *Peer) addCandidatesLocked(endpoints []netip.AddrPort) {
	for _, e := range endpoints {
		p.candidates[e] = &candidate{
			endpoint: e,
		}
	}
	// Prune any candidates not matching new endpoint list
	for ep, _ := range p.candidates {
		if !slices.Contains(endpoints, ep) {
			delete(p.candidates, ep)
		}
	}
}

func (p *Peer) sendEndpoints(msgtype uint8) {
	p.mux.doStun()
	time.Sleep(time.Millisecond * 50)
	ourEndpoints := p.mux.GetEndpoints()
	eps := &Endpoints{Endpoints: ourEndpoints}
	header := p.encodeHeader(msgtype)
	jsonBytes, _ := json.Marshal(eps)
	header = append(header, jsonBytes...)
	p.mux.WriteRelayPacket(header, p.id)
}

func (p *Peer) encodeHeader(msgtype uint8) []byte {
	header := binary.BigEndian.AppendUint32([]byte{}, Magic)
	header = append(header, msgtype)
	return header
}

func (p *Peer) decodeHeader(b []byte) uint8 {
	msgtype := b[4]
	return msgtype
}
