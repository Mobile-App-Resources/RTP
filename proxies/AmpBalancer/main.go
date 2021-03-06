package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/antongulenko/RTP/protocols"
	"github.com/antongulenko/RTP/protocols/amp"
	"github.com/antongulenko/RTP/protocols/balancer"
	"github.com/antongulenko/RTP/protocols/heartbeat"
	"github.com/antongulenko/RTP/protocols/ping"
	"github.com/antongulenko/RTP/proxies/amp_balancer"
	"github.com/antongulenko/golib"
)

var (
	load_servers     = []string{"127.0.0.1:7770"}
	amp_servers      = []string{"127.0.0.1:7777"}
	pcp_servers      = []string{"127.0.0.1:7778", "127.0.0.1:7776"}
	heartbeat_server = "127.0.0.1:0" // Random port
)

func printServerErrors(servername string, server *protocols.Server) {
	for err := range server.Errors() {
		log.Println(servername + " error: " + err.Error())
	}
}

func printSessionStarted(session *protocols.PluginSession) {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "Started session for %v (", session.Client)
	for i, plugin := range session.Plugins {
		if i != 0 {
			fmt.Fprintf(&buf, ", ")
		}
		fmt.Fprintf(&buf, "%v", plugin)
	}
	fmt.Fprintf(&buf, ")")
	log.Println(buf.String())
}

func printSessionStopped(session *protocols.PluginSession) {
	log.Printf("Stopped session for %v\n", session.Client)
}

func stateChangePrinter(key interface{}) {
	breaker, ok := key.(protocols.CircuitBreaker)
	if !ok {
		log.Printf("Failed to convert %v (%T) to CircuitBreaker\n", key, key)
		return
	}
	err, server := breaker.Error(), breaker.String()
	if err != nil {
		log.Printf("%s down: %v\n", server, err)
	} else {
		log.Printf("%s up\n", server)
	}
}

func main() {
	loadBackend := flag.Bool("load", false, "Use Load servers to create the streams, instead of regular AMP Media servers")
	useHeartbeat := flag.Bool("heartbeat", false, "Use heartbeat-based fault detection instead of active ping-based detection")
	_heartbeat_frequency := flag.Uint("heartbeat_frequency", 200, "Time between two heartbeats which observers will send (milliseconds)")
	_heartbeat_timeout := flag.Uint("heartbeat_timeout", 350, "Time between two heartbeats before assuming offline server (milliseconds)")
	amp_addr := protocols.ParseServerFlags("0.0.0.0", 7779)
	heartbeat_frequency := time.Duration(*_heartbeat_frequency) * time.Millisecond
	heartbeat_timeout := time.Duration(*_heartbeat_timeout) * time.Millisecond

	var detector_factory balancer.FaultDetectorFactory
	tasks := golib.NewTaskGroup()
	var heartbeatServer *heartbeat.HeartbeatServer
	if *useHeartbeat {
		var err error
		heartbeatServer, err = heartbeat.NewHeartbeatServer(heartbeat_server)
		golib.Checkerr(err)
		go printServerErrors("Heartbeat", heartbeatServer.Server)
		log.Println("Listening for Heartbeats on", heartbeatServer.LocalAddr())
		detector_factory = func(endpoint string) (protocols.FaultDetector, error) {
			return heartbeatServer.ObserveServer(endpoint, heartbeat_frequency, heartbeat_timeout)
		}
		tasks.AddNamed("heartbeat", heartbeatServer)
	} else {
		detector_factory = func(endpoint string) (protocols.FaultDetector, error) {
			detector, err := ping.DialNewFaultDetector(endpoint)
			if err != nil {
				return nil, err
			}
			detector.Start()
			return detector, nil
		}
	}

	protocol, err := protocols.NewProtocol("AMP", amp.Protocol, ping.Protocol, heartbeat.Protocol)
	golib.Checkerr(err)
	baseServer, err := protocols.NewServer(amp_addr, protocol)
	golib.Checkerr(err)
	server, err := amp_balancer.RegisterPluginServer(baseServer)
	golib.Checkerr(err)
	tasks.AddNamed("server", server)

	ampPlugin := amp_balancer.NewAmpBalancingPlugin(detector_factory)
	server.AddPlugin(ampPlugin)
	pcpPlugin := amp_balancer.NewPcpBalancingPlugin(detector_factory)
	server.AddPlugin(pcpPlugin)

	if *loadBackend {
		for _, load := range load_servers {
			err := ampPlugin.AddBackendServer(load, stateChangePrinter)
			golib.Checkerr(err)
		}
	} else {
		for _, amp := range amp_servers {
			err := ampPlugin.AddBackendServer(amp, stateChangePrinter)
			golib.Checkerr(err)
		}
	}
	for _, pcp := range pcp_servers {
		err := pcpPlugin.AddBackendServer(pcp, stateChangePrinter)
		golib.Checkerr(err)
	}

	go printServerErrors("Server", server.Server)
	server.SessionStartedCallback = printSessionStarted
	server.SessionStoppedCallback = printSessionStopped

	log.Println("Listening to AMP on " + amp_addr)
	log.Println("Press Ctrl-C to close")

	if heartbeatServer != nil {
		tasks.Add(heartbeatServer)
	}
	tasks.Add(&golib.NoopTask{golib.ExternalInterrupt(), "external interrupt"})
	tasks.WaitAndExit()
}
