package service

import (
	"errors"
	"time"

	"sync"

	"github.com/dedis/pulsar/randhound"
	"github.com/dedis/pulsar/randhound/protocol"
	"gopkg.in/dedis/onet.v1"
	"gopkg.in/dedis/onet.v1/log"
	"gopkg.in/dedis/onet.v1/network"
)

// ServiceName ...
const ServiceName = "RandHound"

var randhoundService onet.ServiceID

func init() {
	randhoundService, _ = onet.RegisterNewService(ServiceName, newService)
	network.RegisterMessage(propagateSetup{})
}

// Service ...
type Service struct {
	*onet.ServiceProcessor
	setup      bool
	nodes      int
	groups     int
	purpose    string
	randReady  chan bool
	randLock   sync.Mutex
	random     []byte
	transcript *protocol.Transcript
	interval   int
	tree       *onet.Tree
}

// Setup ...
func (s *Service) Setup(msg *randhound.SetupRequest) (*randhound.SetupReply, onet.ClientError) {

	// Service has already been setup, ignoring further setup requests
	if s.setup == true {
		return nil, onet.NewClientError(errors.New("Randomness service already setup"))
	}
	s.setup = true
	s.tree = msg.Roster.GenerateBinaryTree()

	s.nodes = len(msg.Roster.List)
	s.groups = msg.Groups
	s.purpose = msg.Purpose
	s.interval = msg.Interval

	// This only locks the nodes but does not prevent from using them in
	// another randhound-setup.
	for _, n := range msg.Roster.List {
		if err := s.SendRaw(n, &propagateSetup{}); err != nil {
			return nil, onet.NewClientError(err)
		}
	}
	go s.loop()
	<-s.randReady

	reply := &randhound.SetupReply{}
	return reply, nil
}

// Random accepts client randomness-generation requests, runs the
// RandHound protocol, and returns the collective randomness together with the
// corresponding protocol transcript.
func (s *Service) Random(msg *randhound.RandRequest) (*randhound.RandReply, onet.ClientError) {

	s.randLock.Lock()
	defer s.randLock.Unlock()
	if s.setup == false || s.random == nil {
		return nil, onet.NewClientError(errors.New("Randomness service not setup"))
	}

	return &randhound.RandReply{
		R: s.random,
		T: s.transcript,
	}, nil
}

func (s *Service) propagate(env *network.Envelope) {
	s.setup = true
}

func (s *Service) loop() {
	for {
		err := func() error {
			log.Lvl2("Creating randomness")
			proto, err := s.CreateProtocol(ServiceName, s.tree)
			if err != nil {
				return err
			}
			rh := proto.(*protocol.RandHound)
			if err := rh.Setup(s.nodes, s.groups, s.purpose); err != nil {
				return err
			}

			if err := rh.Start(); err != nil {
				return err
			}

			select {
			case <-rh.Done:

				log.Lvlf1("RandHound - done")

				random, transcript, err := rh.Random()
				if err != nil {
					return err
				}
				log.Lvlf1("RandHound - collective randomness: ok")
				//log.Lvlf1("RandHound - collective randomness: %v", random)

				err = protocol.Verify(rh.Suite(), random, transcript)
				if err != nil {
					return err
				}
				log.Lvlf1("RandHound - verification: ok")

				s.randLock.Lock()
				if s.random == nil {
					s.randReady <- true
				}
				s.random = random
				s.transcript = transcript
				s.randLock.Unlock()

			case <-time.After(time.Second * time.Duration(s.nodes) * 2):
				return err
			}
			return nil
		}()
		if err != nil {
			log.Error("While creating randomness:", err)
		}
		time.Sleep(time.Duration(s.interval) * time.Millisecond)
	}
}

type propagateSetup struct {
}

func newService(c *onet.Context) onet.Service {
	s := &Service{
		ServiceProcessor: onet.NewServiceProcessor(c),
		randReady:        make(chan bool),
	}
	if err := s.RegisterHandlers(s.Setup, s.Random); err != nil {
		log.ErrFatal(err, "RandHound - couldn't register message processing functions")
	}
	s.RegisterProcessorFunc(network.MessageType(propagateSetup{}), s.propagate)
	return s

}
