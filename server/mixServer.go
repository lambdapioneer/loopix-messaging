/*
	Package server implements the mix server.
*/
package server

import (
	"anonymous-messaging/config"
	"anonymous-messaging/helpers"
	"anonymous-messaging/logging"
	"anonymous-messaging/networker"
	"anonymous-messaging/node"
	"anonymous-messaging/sphinx"

	"github.com/protobuf/proto"

	"net"
)

var logLocal = logging.PackageLogger()

type MixServerIt interface {
	networker.NetworkServer
	networker.NetworkClient
}

type MixServer struct {
	id       string
	host     string
	port     string
	listener *net.TCPListener
	*node.Mix

	config config.MixConfig
}

func (m *MixServer) Start() error {
	defer m.run()
	return nil
}

func (m *MixServer) receivedPacket(packet []byte) error {
	logLocal.Info("Received new sphinx packet")

	c := make(chan []byte)
	cAdr := make(chan sphinx.Hop)
	cFlag := make(chan string)
	errCh := make(chan error)

	go m.ProcessPacket(packet, c, cAdr, cFlag, errCh)
	dePacket := <-c
	nextHop := <-cAdr
	flag := <-cFlag
	err := <-errCh

	if err != nil {
		return err
	}

	if flag == "\xF1" {
		m.forwardPacket(dePacket, nextHop.Address)
	} else {
		logLocal.Info("Packet has non-forward flag. Packet dropped")
	}
	return nil
}

func (m *MixServer) forwardPacket(sphinxPacket []byte, address string) error {
	packetBytes, err := config.WrapWithFlag(commFlag, sphinxPacket)
	if err != nil {
		return err
	}
	err = m.send(packetBytes, address)
	if err != nil {
		return err
	}

	return nil
}

func (m *MixServer) send(packet []byte, address string) error {

	conn, err := net.Dial("tcp", address)
	if err != nil {
		return err
	}
	defer conn.Close()

	_, err = conn.Write(packet)
	if err != nil {
		return err
	}
	return nil
}

func (m *MixServer) run() {

	defer m.listener.Close()
	finish := make(chan bool)

	go func() {
		logLocal.Infof("Listening on %s", m.host+":"+m.port)
		m.listenForIncomingConnections()
	}()

	<-finish
}

func (m *MixServer) listenForIncomingConnections() {
	for {
		conn, err := m.listener.Accept()

		if err != nil {
			logLocal.WithError(err).Error(err)
		} else {
			logLocal.Infof("Received connection from %s", conn.RemoteAddr())
			errs := make(chan error, 1)
			go m.handleConnection(conn, errs)
			err = <-errs
			if err != nil {
				logLocal.WithError(err).Error(err)
			}
		}
	}
}

func (m *MixServer) handleConnection(conn net.Conn, errs chan<- error) {
	defer conn.Close()

	buff := make([]byte, 1024)
	reqLen, err := conn.Read(buff)
	if err != nil {
		errs <- err
	}

	var packet config.GeneralPacket
	err = proto.Unmarshal(buff[:reqLen], &packet)
	if err != nil {
		errs <- err
	}

	switch packet.Flag {
	case commFlag:
		err = m.receivedPacket(packet.Data)
		if err != nil {
			errs <- err
		}
	default:
		logLocal.Infof("Packet flag %s not recognised. Packet dropped", packet.Flag)
	}
}

func NewMixServer(id, host, port string, pubKey []byte, prvKey []byte, pkiPath string) (*MixServer, error) {
	mix := node.NewMix(pubKey, prvKey)
	mixServer := MixServer{id: id, host: host, port: port, Mix: mix, listener: nil}
	mixServer.config = config.MixConfig{Id: mixServer.id, Host: mixServer.host, Port: mixServer.port, PubKey: mixServer.GetPublicKey()}

	configBytes, err := proto.Marshal(&mixServer.config)
	if err != nil {
		return nil, err
	}
	err = helpers.AddToDatabase(pkiPath, "Pki", mixServer.id, "Mix", configBytes)
	if err != nil {
		return nil, err
	}

	addr, err := helpers.ResolveTCPAddress(mixServer.host, mixServer.port)

	if err != nil {
		return nil, err
	}
	mixServer.listener, err = net.ListenTCP("tcp", addr)

	if err != nil {
		return nil, err
	}

	return &mixServer, nil
}
