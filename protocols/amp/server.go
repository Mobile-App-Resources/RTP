package amp

import (
	"fmt"

	"github.com/antongulenko/RTP/protocols"
)

type Server struct {
	*protocols.Server
	*ampProtocol
	handler Handler
}

type Handler interface {
	StartStream(val *StartStream) error
	StopStream(val *StopStream) error
	StopServer()
}

func NewServer(local_addr string, handler Handler) (server *Server, err error) {
	if handler == nil {
		return nil, fmt.Errorf("Need non-nil amp.Handler")
	}
	server = &Server{handler: handler}
	server.Server, err = protocols.NewServer(local_addr, server)
	if err != nil {
		server = nil
	}
	return
}

func (server *Server) StopServer() {
	server.handler.StopServer()
}

func (server *Server) HandleRequest(packet *protocols.Packet) {
	val := packet.Val
	switch packet.Code {
	case CodeStartStream:
		if desc, ok := val.(*StartStream); ok {
			server.ReplyCheck(packet, server.handler.StartStream(desc))
		} else {
			server.ReplyError(packet, fmt.Errorf("Illegal value for AMP StartStream: %v", packet.Val))
		}
	case CodeStopStream:
		if desc, ok := val.(*StopStream); ok {
			server.ReplyCheck(packet, server.handler.StopStream(desc))
		} else {
			server.ReplyError(packet, fmt.Errorf("Illegal value for AMP StopStream: %v", packet.Val))
		}
	default:
		server.LogError(fmt.Errorf("Received unexpected AMP code: %v", packet.Code))
	}
}