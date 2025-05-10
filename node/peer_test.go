package node

import (
	"calnet_server/keys"
	"calnet_server/testrelay"
	"fmt"
	"net/netip"
	"os"
	"testing"
	"time"
)

var (
	tk1       keys.PrivateKey
	tk2       keys.PrivateKey
	nodeID1   uint64 = 1
	nodeID2   uint64 = 2
	RelayPort        = 9090
	mux1      *UdpMux
	mux2      *UdpMux
	rc1       *RelayClient
	rc2       *RelayClient
	peer1     *Peer
	peer2     *Peer
)

// Leave tunnel nil for testing
// Add peer method to initate a test data packet to kick off the process
// Add a special magic value to cause the peer to return the packet so it bounces back and forth until stopped
func TestMain(m *testing.M) {

	tk1 = keys.NewPrivateKey()
	tk2 = keys.NewPrivateKey()
	go testrelay.StartRelayTestServer(tk1.PublicKey(), tk2.PublicKey(), RelayPort)
	rc1 = NewRelayClient(fmt.Sprintf("http://127.0.0.1:%d", RelayPort), tk1.PublicKey())
	rc2 = NewRelayClient(fmt.Sprintf("http://127.0.0.1:%d", RelayPort), tk2.PublicKey())

	mux1 = NewUdpMux(&UdpMuxOpts{NodeID: nodeID1, RelayClient: rc1})
	mux2 = NewUdpMux(&UdpMuxOpts{NodeID: nodeID2, RelayClient: rc2})

	mux1.Bind()
	mux2.Bind()

	peer1 = NewPeer(nodeID2, netip.MustParseAddr("100.70.0.1"), mux1, nil)
	peer2 = NewPeer(nodeID1, netip.MustParseAddr("100.70.0.1"), mux2, nil)

	mux1.RegisterPeer(peer1)
	mux2.RegisterPeer(peer2)

	os.Exit(m.Run())
}

func TestPeerTraffic(t *testing.T) {
	ti := time.NewTimer(time.Second * 20)

	peer1.sendMagicTestPacket()

	<-ti.C
}
