package randhound

import (
	"github.com/dedis/pulsar/randhound/protocol"
	"gopkg.in/dedis/onet.v1"
	"gopkg.in/dedis/onet.v1/network"
)

// Register messages to the network.
func init() {
	network.RegisterMessage(SetupRequest{})
	network.RegisterMessage(SetupReply{})
	network.RegisterMessage(RandRequest{})
	network.RegisterMessage(RandReply{})
}

const (
	ErrorInternal = 4000 + iota
	ErrorParameter
)

// SetupRequest ...
type SetupRequest struct {
	Roster   *onet.Roster
	Groups   int
	Purpose  string
	Interval int // the interval in milliseconds between to random-generations
}

// SetupReply ...
type SetupReply struct {
}

// RandRequest sent from client to randomness service to request collective randomness.
// If Index > 0, it will search for the skipblock with that index.
type RandRequest struct {
	Index int
}

// RandReply sent from randomness service to client to return collective randomness.
type RandReply struct {
	R     []byte
	T     *protocol.Transcript
	Index int
}
