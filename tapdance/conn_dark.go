package tapdance

import (
	"bufio"
	"context"
	"fmt"
	"github.com/refraction-networking/utls"
	"net"
	"net/http"
	"net/http/httputil"
	"time"
)


func dialDarkDecoy(ctx context.Context, tdFlow *TapdanceFlowConn) (net.Conn, error) {
//	_ddIpSelector, err := newDDIpSelector([]string{"192.122.190.0/24", "2001:48a8:8000::/64"})
	_ddIpSelector, err := newDDIpSelector([]string{"192.122.190.0/24"})
	if err != nil {
		return nil, err
	}

	darkDecoySNI := pickDarkDecoySNI()

	tdFlow.tdRaw.tagType = tagHttpGetComplete
	tdFlow.flowType = flowRendezvous
	tdFlow.tdRaw.darkDecoyUsed = true
	tdFlow.tdRaw.darkDecoySNI = darkDecoySNI

	err = tdFlow.DialContext(ctx)
	if err != nil {
		return nil, err
	}
	go readAndClose(tdFlow, time.Second * 15)

	darDecoyIpAddr, err := _ddIpSelector.selectIpAddr(tdFlow.tdRaw.tdKeys.DarkDecoySeed)
	if err != nil {
		Logger().Infof( "%v failed to select dark decoy: %v\n", tdFlow.idStr(), err)
		return nil, err
	}

	getRttMillisec := func() int {
		defaultValue := 300
		if tdFlow == nil {
			return defaultValue
		}
		if tdFlow.tdRaw == nil {
			return defaultValue
		}
		if tdFlow.tdRaw.sessionStats.TcpToDecoy == nil {
			return defaultValue
		}
		return int(*tdFlow.tdRaw.sessionStats.TcpToDecoy)
		}
	// randomized sleeping here to break the intraflow signal
	toSleep := time.Millisecond * time.Duration(300 + getRttMillisec() * getRandInt(212, 3449) / 1000)
	Logger().Debugf("%v Registration for dark decoy sent, sleeping for %v", tdFlow.idStr(), toSleep)
	time.Sleep(toSleep)

	darkAddr := net.JoinHostPort(darDecoyIpAddr.String(), "443")
	Logger().Infof(  "%v Connecting to dark decoy %v", tdFlow.idStr(), darkAddr)
	darkTcpConn, err := net.DialTimeout("tcp", darkAddr, 10 * time.Second)
	if err != nil {
		Logger().Infof( "%v failed to dial dark decoy %v: %v\n",
			tdFlow.idStr(), darDecoyIpAddr.String(), err)
		return nil, err
	}

	darkTlsConn := tls.UClient(darkTcpConn, &tls.Config{ServerName: darkDecoySNI},
	tls.HelloRandomizedNoALPN)
	err = darkTlsConn.Handshake()
	if err != nil {
		Logger().Infof( "%v failed to do tls handshake with dark decoy %v(%v): %v\n",
			tdFlow.idStr(), darDecoyIpAddr.String(), darkDecoySNI, err)
		return nil, err
	}

	darkTlsConn.Conn = nil
	forgedTlsConn := tls.MakeConnWithCompleteHandshake(
		darkTcpConn, tls.VersionTLS12, // TODO: parse version! :(
		darkTlsConn.HandshakeState.ServerHello.CipherSuite,
		tdFlow.tdRaw.tdKeys.NewMasterSecret,
		darkTlsConn.HandshakeState.Hello.Random[:],
		darkTlsConn.HandshakeState.ServerHello.Random[:], true)
	// 	return forgedTlsConn, nil
	// TODO: remove everything below, it is just a test case

	targetTlsConn := tls.UClient(forgedTlsConn,
		&tls.Config{ServerName: "twitter.com",},
		tls.HelloRandomizedNoALPN)

	err = targetTlsConn.Handshake()
	if err != nil {
		Logger().Infof( "%v failed to do tls handshake with target %v: %v\n",
			tdFlow.idStr(), "twitter.com", err)
		return nil, err
	}

	_, err = targetTlsConn.Write([]byte("GET / HTTP/1.1\r\nHost: twitter.com\r\n\r\n"))
	if err != nil {
		Logger().Infof( "%v failed to targetTlsConn.Write: %v\n",
			tdFlow.idStr(), err)
		return nil, err
	}

	resp, err := http.ReadResponse(bufio.NewReader(targetTlsConn), nil)
	if err != nil {
		Logger().Infof( "%v failed to http.ReadResponse: %v\n",
			tdFlow.idStr(), err)
		return nil, err
	}

	bResp, err := httputil.DumpResponse(resp, true)
	fmt.Println(string(bResp))
	if err != nil {
		Logger().Infof( "%v failed to DumpResponse: %v\n",
			tdFlow.idStr(), err)
		return nil, err
	}

	return forgedTlsConn, nil
}
