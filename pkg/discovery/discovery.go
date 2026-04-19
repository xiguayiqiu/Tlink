package discovery

import (
	"fmt"
	"net"
	"sync"
	"time"
	"tlink/pkg/protocol"
)

const (
	UDP_PORT           = 9999
	BROADCAST_INTERVAL = 2 * time.Second
	DISCOVERY_TIMEOUT  = 10 * time.Second
)

type DiscoveryService struct {
	uid        string
	deviceName string
	tcpPort    int
	fileName   string
	fileSize   int64
	isSender   bool
	conn       *net.UDPConn
	peers      map[string]*PeerInfo
	peersMutex sync.RWMutex
	stopChan   chan struct{}
	peerUpdate chan struct{}
	started    bool
}

type PeerInfo struct {
	UID        string
	DeviceName string
	Address    *net.UDPAddr
	TCPPort    int
	FileName   string
	FileSize   int64
	LastSeen   time.Time
}

func NewDiscoveryService(uid, deviceName string, tcpPort int, fileName string, fileSize int64, isSender bool) *DiscoveryService {
	return &DiscoveryService{
		uid:        uid,
		deviceName: deviceName,
		tcpPort:    tcpPort,
		fileName:   fileName,
		fileSize:   fileSize,
		isSender:   isSender,
		peers:      make(map[string]*PeerInfo),
		stopChan:   make(chan struct{}),
		peerUpdate: make(chan struct{}, 1),
	}
}

func (ds *DiscoveryService) Start() error {
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", UDP_PORT))
	if err != nil {
		return err
	}

	ds.conn, err = net.ListenUDP("udp", addr)
	if err != nil {
		return err
	}

	ds.started = true
	go ds.receiveLoop()

	if ds.isSender {
		go ds.broadcastLoop()
	}

	go ds.cleanupLoop()

	return nil
}

func (ds *DiscoveryService) Stop() {
	close(ds.stopChan)
	if ds.conn != nil {
		ds.conn.Close()
	}
}

func (ds *DiscoveryService) GetPeers() []*PeerInfo {
	ds.peersMutex.RLock()
	defer ds.peersMutex.RUnlock()

	peers := make([]*PeerInfo, 0, len(ds.peers))
	for _, peer := range ds.peers {
		peers = append(peers, peer)
	}
	return peers
}

func (ds *DiscoveryService) PeerUpdateChan() <-chan struct{} {
	return ds.peerUpdate
}

func (ds *DiscoveryService) broadcastLoop() {
	ticker := time.NewTicker(BROADCAST_INTERVAL)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			ds.broadcast()
		case <-ds.stopChan:
			return
		}
	}
}

func (ds *DiscoveryService) broadcast() {
	if !ds.started || ds.conn == nil {
		return
	}

	broadcastAddrs, err := ds.getBroadcastAddresses()
	if err != nil {
		return
	}

	data := protocol.DiscoveryData{
		UID:        ds.uid,
		DeviceName: ds.deviceName,
		TCPPort:    ds.tcpPort,
		FileName:   ds.fileName,
		FileSize:   ds.fileSize,
	}

	msg := protocol.NewMessage(protocol.MsgTypeDiscovery, data)
	jsonData, err := msg.ToJSON()
	if err != nil {
		return
	}

	for _, addr := range broadcastAddrs {
		udpAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", addr, UDP_PORT))
		if err != nil {
			continue
		}
		ds.conn.WriteToUDP(jsonData, udpAddr)
	}
}

func (ds *DiscoveryService) receiveLoop() {
	if !ds.started || ds.conn == nil {
		return
	}

	buffer := make([]byte, 4096)
	for {
		select {
		case <-ds.stopChan:
			return
		default:
			ds.conn.SetReadDeadline(time.Now().Add(1 * time.Second))
			n, addr, err := ds.conn.ReadFromUDP(buffer)
			if err != nil {
				continue
			}

			msg, err := protocol.FromJSON(buffer[:n])
			if err != nil {
				continue
			}

			if msg.Type == protocol.MsgTypeDiscovery {
				ds.handleDiscovery(msg, addr)
			}
		}
	}
}

func (ds *DiscoveryService) handleDiscovery(msg *protocol.Message, addr *net.UDPAddr) {
	data, err := protocol.ParseDiscoveryData(msg.Data)
	if err != nil {
		return
	}

	if data.UID == ds.uid {
		return
	}

	ds.peersMutex.Lock()
	ds.peers[data.UID] = &PeerInfo{
		UID:        data.UID,
		DeviceName: data.DeviceName,
		Address:    addr,
		TCPPort:    data.TCPPort,
		FileName:   data.FileName,
		FileSize:   data.FileSize,
		LastSeen:   time.Now(),
	}
	ds.peersMutex.Unlock()

	select {
	case ds.peerUpdate <- struct{}{}:
	default:
	}
}

func (ds *DiscoveryService) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			ds.cleanupPeers()
		case <-ds.stopChan:
			return
		}
	}
}

func (ds *DiscoveryService) cleanupPeers() {
	ds.peersMutex.Lock()
	defer ds.peersMutex.Unlock()

	now := time.Now()
	updated := false

	for uid, peer := range ds.peers {
		if now.Sub(peer.LastSeen) > DISCOVERY_TIMEOUT {
			delete(ds.peers, uid)
			updated = true
		}
	}

	if updated {
		select {
		case ds.peerUpdate <- struct{}{}:
		default:
		}
	}
}

func (ds *DiscoveryService) getBroadcastAddresses() ([]string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	var addrs []string
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrsList, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrsList {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP.To4() == nil {
				continue
			}

			broadcast := make(net.IP, len(ipNet.IP.To4()))
			for i := 0; i < 4; i++ {
				broadcast[i] = ipNet.IP.To4()[i] | ^ipNet.Mask[i]
			}
			addrs = append(addrs, broadcast.String())
		}
	}

	if len(addrs) == 0 {
		addrs = append(addrs, "255.255.255.255")
	}

	return addrs, nil
}
