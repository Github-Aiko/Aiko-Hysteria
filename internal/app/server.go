package app

import (
	"crypto/tls"
	"io"
	"net"
	"time"

	"github.com/Github-Aiko/Aiko-Hysteria/internal/app/service"
	"github.com/Github-Aiko/Aiko-Hysteria/internal/pkg/core"
	"github.com/Github-Aiko/Aiko-Hysteria/internal/pkg/pmtud"
	"github.com/Github-Aiko/Aiko-Hysteria/internal/pkg/transport"
	"github.com/Github-Aiko/Aiko-Hysteria/internal/pkg/transport/pktconns"
	"github.com/Github-Aiko/Aiko-Hysteria/internal/pkg/utils"
	"github.com/quic-go/quic-go"
	"github.com/sirupsen/logrus"
)

var defaultIPMasker = &utils.IpMasker{}

var serverPacketConnFuncFactoryMap = map[string]pktconns.ServerPacketConnFuncFactory{
	"":             pktconns.NewServerUDPConnFunc,
	"udp":          pktconns.NewServerUDPConnFunc,
	"wechat":       pktconns.NewServerWeChatConnFunc,
	"wechat-video": pktconns.NewServerWeChatConnFunc,
	"faketcp":      pktconns.NewServerFakeTCPConnFunc,
}

func Run(config *ServerConfig, usersService *service.UsersService) {
	logrus.WithField("config", config.String()).Info("Server configuration loaded")
	config.Fill()

	if err := usersService.Init(); err != nil {
		logrus.Fatalf("User service initialization error：%s", err)
	}

	// Load TLS config
	var tlsConfig *tls.Config
	// Local cert mode
	kpl, loaderErr := utils.NewKeypairLoader(config.CertFile, config.KeyFile)
	if loaderErr != nil {
		logrus.WithFields(logrus.Fields{
			"error": loaderErr,
			"cert":  config.CertFile,
			"key":   config.KeyFile,
		}).Fatal("Failed to load the certificate")
	}
	tlsConfig = &tls.Config{
		GetCertificate: kpl.GetCertificateFunc(),
		NextProtos:     []string{config.ALPN},
		MinVersion:     tls.VersionTLS13,
	}

	// QUIC config
	quicConfig := &quic.Config{
		InitialStreamReceiveWindow:     config.ReceiveWindowConn,
		MaxStreamReceiveWindow:         config.ReceiveWindowConn,
		InitialConnectionReceiveWindow: config.ReceiveWindowClient,
		MaxConnectionReceiveWindow:     config.ReceiveWindowClient,
		MaxIncomingStreams:             int64(config.MaxConnClient),
		MaxIdleTimeout:                 ServerMaxIdleTimeoutSec * time.Second,
		KeepAlivePeriod:                10 * time.Second, // Keep alive should solely be client's responsibility
		DisablePathMTUDiscovery:        config.DisableMTUDiscovery,
		EnableDatagrams:                true,
	}

	if !quicConfig.DisablePathMTUDiscovery && pmtud.DisablePathMTUDiscovery {
		logrus.Info("Path MTU Discovery is not yet supported on this platform")
	}
	// Auth
	var authFunc core.ConnectFunc
	var err error
	// Auth func
	authFunc = func(addr net.Addr, auth []byte, sSend uint64, sRecv uint64) (bool, int) {
		userId, ok := usersService.Auth(string(auth))
		return ok, userId
	}

	connectFunc := func(addr net.Addr, auth []byte, sSend uint64, sRecv uint64) (bool, int) {
		ok, userId := authFunc(addr, auth, sSend, sRecv)
		if !ok {
			logrus.WithFields(logrus.Fields{
				"src": defaultIPMasker.Mask(addr.String()),
				"msg": userId,
			}).Info("Authentication failed, client rejected")
		} else {
			logrus.WithFields(logrus.Fields{
				"src": defaultIPMasker.Mask(addr.String()),
			}).Info("Client connected")
		}
		return ok, userId
	}

	// Packet conn
	pktConnFuncFactory := serverPacketConnFuncFactoryMap[config.Protocol]
	if pktConnFuncFactory == nil {
		logrus.WithField("protocol", config.Protocol).Fatal("Unsupported protocol")
	}
	pktConnFunc := pktConnFuncFactory(config.Obfs)
	pktConn, err := pktConnFunc(config.Listen)
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"error": err,
			"addr":  config.Listen,
		}).Fatal("Failed to listen on the UDP address")
	}
	// Server
	up, down, _ := config.Speed()
	server, err := core.NewServer(tlsConfig, quicConfig, pktConn,
		transport.DefaultServerTransport, up, down, config.DisableUDP, usersService,
		connectFunc, disconnectFunc, tcpRequestFunc, tcpErrorFunc, udpRequestFunc, udpErrorFunc)
	if err != nil {
		logrus.WithField("error", err).Fatal("Failed to initialize server")
	}
	defer usersService.Close()
	defer server.Close()
	logrus.WithField("addr", config.Listen).Info("Server up and running")

	if err := usersService.Start(); err != nil {
		logrus.Fatalf("User service start error：%s", err)
	}
	err = server.Serve()
	logrus.WithField("error", err).Fatal("Server shutdown")
}

func disconnectFunc(addr net.Addr, userId int, err error) {
	logrus.WithFields(logrus.Fields{
		"src":    defaultIPMasker.Mask(addr.String()),
		"error":  err,
		"userId": userId,
	}).Info("Client disconnected")
}

func tcpRequestFunc(addr net.Addr, userId int, reqAddr string) {
	logrus.WithFields(logrus.Fields{
		"src":    defaultIPMasker.Mask(addr.String()),
		"dst":    defaultIPMasker.Mask(reqAddr),
		"userId": userId,
	}).Debug("TCP request")
}

func tcpErrorFunc(addr net.Addr, userId int, reqAddr string, err error) {
	if err != io.EOF {
		logrus.WithFields(logrus.Fields{
			"src":    defaultIPMasker.Mask(addr.String()),
			"dst":    defaultIPMasker.Mask(reqAddr),
			"error":  err,
			"userId": userId,
		}).Info("TCP error")
	} else {
		logrus.WithFields(logrus.Fields{
			"src": defaultIPMasker.Mask(addr.String()),
			"dst": defaultIPMasker.Mask(reqAddr),
		}).Debug("TCP EOF")
	}
}

func udpRequestFunc(addr net.Addr, userId int, sessionID uint32) {
	logrus.WithFields(logrus.Fields{
		"src":     defaultIPMasker.Mask(addr.String()),
		"session": sessionID,
		"userId":  userId,
	}).Debug("UDP request")
}

func udpErrorFunc(addr net.Addr, userId int, sessionID uint32, err error) {
	if err != io.EOF {
		logrus.WithFields(logrus.Fields{
			"src":     defaultIPMasker.Mask(addr.String()),
			"session": sessionID,
			"error":   err,
			"userId":  userId,
		}).Info("UDP error")
	} else {
		logrus.WithFields(logrus.Fields{
			"src":     defaultIPMasker.Mask(addr.String()),
			"session": sessionID,
			"userId":  userId,
		}).Debug("UDP EOF")
	}
}
