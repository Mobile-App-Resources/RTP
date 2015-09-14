package main

import (
	"log"

	. "github.com/antongulenko/RTP/helpers"
	"github.com/antongulenko/RTP/protocols"
	"github.com/antongulenko/RTP/proxies"
)

const (
	amp_addr = "127.0.0.1:7779"
)

func printAmpErrors(balancer *proxies.AmpBalancer) {
	for err := range balancer.Errors() {
		log.Println("AMP error: " + err.Error())
	}
}

func printSessionStarted(client, server string) {
	log.Println("Started session for", client, "at", server)
}

func printSessionStopped(client, server string) {
	log.Println("Stopped session for", client, "at", server)
}

func stateChangePrinter(breaker protocols.CircuitBreaker) {
	err, server := breaker.Error(), breaker.Server()
	if err != nil {
		log.Printf("AMP Server %v down: %v\n", server, err)
	} else {
		log.Printf("AMP Server %v up\n", server)
	}
}

func main() {
	balancer, err := proxies.NewAmpBalancer(amp_addr)
	Checkerr(err)

	go printAmpErrors(balancer)
	balancer.SessionStartedCallback = printSessionStarted
	balancer.SessionStoppedCallback = printSessionStopped
	_, err = balancer.AddMediaServer("127.0.0.1:7777", stateChangePrinter)
	Checkerr(err)

	balancer.Start()

	log.Println("Listening to AMP on " + amp_addr)
	log.Println("Press Ctrl-C to close")
	WaitAndStopObservees(nil, []Observee{
		balancer,
		&NoopObservee{ExternalInterrupt()},
	})
}
