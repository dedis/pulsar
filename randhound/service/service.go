package service

import (
	"errors"
	"time"

	"sync"

	"github.com/dedis/cothority/skipchain"
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
	network.RegisterMessage(storage{})
	network.RegisterMessage(PulsarDef{})
}

// Service ...
type Service struct {
	*onet.ServiceProcessor
	randReady chan bool
	latest    *skipchain.SkipBlock
	pulsarDef *PulsarDef
	storage   *storage
}

const storageID = "main"

type storage struct {
	sync.Mutex
	Setup   bool
	Genesis *skipchain.SkipBlock
}

// PulsarDef is stored in the Genesis-skipblock
type PulsarDef struct {
	Groups   int
	Purpose  string
	Interval int
}

// Setup ...
func (s *Service) Setup(msg *randhound.SetupRequest) (*randhound.SetupReply, onet.ClientError) {

	// Service has already been setup, ignoring further setup requests
	if s.storage.Setup == true {
		return nil, onet.NewClientError(errors.New("Randomness service already setup"))
	}
	s.storage = &storage{
		Setup: true,
	}

	s.pulsarDef = &PulsarDef{
		Groups:   msg.Groups,
		Purpose:  msg.Purpose,
		Interval: msg.Interval,
	}

	c := skipchain.NewClient()
	var cerr onet.ClientError
	s.storage.Genesis, cerr = c.CreateGenesis(msg.Roster, 1, 1, skipchain.VerificationNone,
		s.pulsarDef, nil)
	if cerr != nil {
		return nil, cerr
	}

	// This only locks the nodes but does not prevent from using them in
	// another randhound-setup.
	for _, n := range msg.Roster.List {
		if err := s.SendRaw(n, &propagateSetup{
			Genesis: s.storage.Genesis,
		}); err != nil {
			return nil, onet.NewClientError(err)
		}
	}
	go s.loop()
	<-s.randReady

	reply := &randhound.SetupReply{}
	s.save()
	return reply, nil
}

// Random fetches the latest skipblock and returns the random value.
func (s *Service) Random(msg *randhound.RandRequest) (*randhound.RandReply, onet.ClientError) {
	s.storage.Lock()
	defer s.storage.Unlock()

	if s.storage.Setup == false {
		return nil, onet.NewClientError(errors.New("Randomness service not setup"))
	}

	cl := skipchain.NewClient()
	rep, cerr := cl.GetUpdateChain(s.storage.Genesis.Roster, s.storage.Genesis.Hash)
	if cerr != nil {
		return nil, onet.NewClientErrorCode(randhound.ErrorInternal, "error while updating skipchain")
	}
	if len(rep.Update) == 0 {
		return nil, onet.NewClientErrorCode(randhound.ErrorInternal, "no random values yet")
	}
	s.latest = rep.Update[len(rep.Update)-1]

	if msg.Index > len(rep.Update) || msg.Index < 0 {
		return nil, onet.NewClientErrorCode(randhound.ErrorParameter, "invalid index")
	}

	indexed := rep.Update[msg.Index]
	if msg.Index == 0 {
		indexed = s.latest
	}
	log.Lvl2("Got random-request for index", msg.Index, "and will send index", indexed.Index)
	_, rrInt, err := network.Unmarshal(indexed.Data)
	if err != nil {
		return nil, onet.NewClientErrorCode(randhound.ErrorInternal, "couldn't unmarshal skipblock-data")
	}
	rr, ok := rrInt.(*randhound.RandReply)
	if !ok {
		return nil, onet.NewClientErrorCode(randhound.ErrorInternal, "wrong data-type in skipblock")
	}
	rr.Index = indexed.Index
	log.Lvlf2("Sending %+v", rr)
	return rr, nil
}

func (s *Service) propagate(env *network.Envelope) {
	s.storage.Lock()
	defer s.storage.Unlock()
	s.storage.Setup = true
}

func (s *Service) loop() {
	cl := skipchain.NewClient()
	rep, cerr := cl.GetUpdateChain(s.storage.Genesis.Roster, s.storage.Genesis.Hash)
	if cerr != nil {
		log.Error(cerr)
	}
	if len(rep.Update) == 0 {
		log.Error("no updates yet")
	}
	s.latest = rep.Update[len(rep.Update)-1]
	log.Lvl1(s.ServerIdentity(), "updated to", s.latest.Index)
	s.save()
	for {
		err := func() error {
			log.Lvl2("Creating randomness")
			t := s.storage.Genesis.Roster.GenerateBinaryTree()
			proto, err := s.CreateProtocol(ServiceName, t)
			if err != nil {
				return err
			}
			rh := proto.(*protocol.RandHound)
			if err := rh.Setup(t.Size(), s.pulsarDef.Groups, s.pulsarDef.Purpose); err != nil {
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

				s.storage.Lock()
				if s.latest == nil || s.latest.Index == 0 {
					s.randReady <- true
					s.latest = s.storage.Genesis
				}

				rr := &randhound.RandReply{
					R: random,
					T: transcript,
				}
				rep, cerr := cl.StoreSkipBlock(s.latest, nil, rr)
				if cerr != nil {
					log.Error("Couldn't store new skipblock:", cerr)
				} else {
					s.latest = rep.Latest
				}

				s.storage.Unlock()

			case <-time.After(time.Second * time.Duration(t.Size()) * 2):
				return err
			}
			return nil
		}()
		if err != nil {
			log.Error("While creating randomness:", err)
		}
		s.save()
		time.Sleep(time.Duration(s.pulsarDef.Interval) * time.Millisecond)
	}
}

type propagateSetup struct {
	Genesis *skipchain.SkipBlock
}

// saves all skipblocks.
func (s *Service) save() {
	s.storage.Lock()
	defer s.storage.Unlock()
	err := s.Save(storageID, s.storage)
	if err != nil {
		log.Error("Couldn't save file:", err)
	}
}

// Tries to load the configuration and updates the data in the service
// if it finds a valid config-file.
func (s *Service) tryLoad() error {
	s.storage = &storage{}
	if !s.DataAvailable(storageID) {
		return nil
	}
	msg, err := s.Load(storageID)
	if err != nil {
		return err
	}
	var ok bool
	s.storage, ok = msg.(*storage)
	if !ok {
		return errors.New("Data of wrong type")
	}

	if s.storage.Genesis != nil {
		_, pdInt, err := network.Unmarshal(s.storage.Genesis.Data)
		if err != nil {
			return err
		}
		s.pulsarDef, ok = pdInt.(*PulsarDef)
		if !ok {
			return errors.New("couldn't recover pulsarDef from genesis-block")
		}
		s.latest = s.storage.Genesis

		go s.loop()
	}

	return nil
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
	if err := s.tryLoad(); err != nil {
		log.Error(s.ServerIdentity(), err)
	}
	return s

}
