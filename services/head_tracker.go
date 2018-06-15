package services

import (
	"errors"
	"fmt"
	"sync"

	"github.com/asdine/storm"
	uuid "github.com/satori/go.uuid"
	"github.com/smartcontractkit/chainlink/logger"
	"github.com/smartcontractkit/chainlink/store"
	"github.com/smartcontractkit/chainlink/store/models"
	"github.com/smartcontractkit/chainlink/store/presenters"
	"github.com/smartcontractkit/chainlink/utils"
)

// HeadTrackable represents any object that wishes to respond to ethereum events,
// after being attached to HeadTracker.
type HeadTrackable interface {
	Connect(*models.IndexableBlockNumber) error
	Disconnect()
	OnNewHead(*models.BlockHeader)
}

// HeadTracker holds and stores the latest block number experienced by this particular node
// in a thread safe manner. Reconstitutes the last block number from the data
// store on reboot.
type HeadTracker struct {
	connected              bool
	mutex                  sync.RWMutex
	trackersMutex          sync.RWMutex
	listenToNewHeadsCloser chan struct{}
	listenToNewHeadsWaiter sync.WaitGroup
	number                 *models.IndexableBlockNumber
	sleeper                utils.Sleeper
	store                  *store.Store
	trackers               map[string]HeadTrackable
}

// NewHeadTracker instantiates a new HeadTracker using the orm to persist new block numbers.
// Can be passed in an optional sleeper object that will dictate how often
// it tries to reconnect.
func NewHeadTracker(store *store.Store, sleepers ...utils.Sleeper) *HeadTracker {
	var sleeper utils.Sleeper
	if len(sleepers) > 0 {
		sleeper = sleepers[0]
	} else {
		sleeper = utils.NewBackoffSleeper()
	}
	return &HeadTracker{store: store, trackers: map[string]HeadTrackable{}, sleeper: sleeper}
}

// Start retrieves the last persisted block number from the HeadTracker,
// subscribes to new heads, and if successful fires Connect on the
// HeadTrackable argument.
func (ht *HeadTracker) Start() error {
	ht.mutex.Lock()
	defer ht.mutex.Unlock()

	if ht.listenToNewHeadsCloser != nil {
		return errors.New("HeadTracker already started")
	}
	ht.listenToNewHeadsCloser = make(chan struct{})

	numbers := []models.IndexableBlockNumber{}
	err := ht.store.Select().OrderBy("Digits", "Number").Limit(1).Reverse().Find(&numbers)
	if err != nil && err != storm.ErrNotFound {
		return err
	}
	if len(numbers) > 0 {
		ht.number = &numbers[0]
	}
	if ht.number != nil {
		logger.Debug("Tracking logs from last block ", presenters.FriendlyBigInt(ht.number.ToInt()), " with hash ", ht.number.Hash.String())
	}

	headers, sub, err := ht.newHeadListener()
	if err != nil {
		return err
	}
	go ht.listenToNewHeads(headers, sub)

	go ht.updateBlockHeader()
	return nil
}

// Stop unsubscribes all connections and fires Disconnect.
func (ht *HeadTracker) Stop() error {
	ht.mutex.Lock()
	defer ht.mutex.Unlock()

	if ht.listenToNewHeadsCloser == nil {
		return errors.New("HeadTracker already stopped")
	}

	//fmt.Println("Stopping...")
	ht.listenToNewHeadsCloser <- struct{}{}
	ht.listenToNewHeadsWaiter.Wait()
	ht.listenToNewHeadsCloser = nil
	//fmt.Println("Stopped!")
	return nil
}

// Save updates the latest block number, if indeed the latest, and persists
// this number in case of reboot. Thread safe.
func (ht *HeadTracker) Save(n *models.IndexableBlockNumber) error {
	if n == nil {
		return errors.New("Cannot save a nil block header")
	}

	ht.mutex.Lock()
	defer ht.mutex.Unlock()
	if n.GreaterThan(ht.number) {
		copy := *n
		ht.number = &copy
	}
	return ht.store.Save(n)
}

// LastRecord returns the latest block header being tracked, or nil.
func (ht *HeadTracker) LastRecord() *models.IndexableBlockNumber {
	ht.mutex.RLock()
	defer ht.mutex.RUnlock()
	return ht.number
}

// Attach registers an object that will have HeadTrackable events fired on occurence,
// such as Connect.
func (ht *HeadTracker) Attach(t HeadTrackable) string {
	id := uuid.Must(uuid.NewV4()).String()

	ht.trackersMutex.Lock()
	defer ht.trackersMutex.Unlock()
	ht.trackers[id] = t
	if ht.connected {
		t.Connect(ht.LastRecord())
	}
	return id
}

// Detach deregisters an object from having HeadTrackable events fired.
func (ht *HeadTracker) Detach(id string) {
	ht.trackersMutex.Lock()
	defer ht.trackersMutex.Unlock()
	t, present := ht.trackers[id]
	if ht.connected && present {
		t.Disconnect()
	}
	delete(ht.trackers, id)
}

// IsConnected returns whether or not this HeadTracker is connected.
func (ht *HeadTracker) IsConnected() bool {
	ht.trackersMutex.RLock()
	defer ht.trackersMutex.RUnlock()
	return ht.connected
}

func (ht *HeadTracker) connect(bn *models.IndexableBlockNumber) {
	//fmt.Println("connect()")
	ht.trackersMutex.RLock()
	defer ht.trackersMutex.RUnlock()
	ht.connected = true
	for _, t := range ht.trackers {
		//fmt.Println("Sending connected signal to", t)
		logger.WarnIf(t.Connect(bn))
	}
}

func (ht *HeadTracker) disconnect() {
	//fmt.Println("disconnect()")
	ht.trackersMutex.RLock()
	defer ht.trackersMutex.RUnlock()
	ht.connected = false
	//fmt.Println("...")
	for _, t := range ht.trackers {
		//fmt.Println("Sending disconnected signal to", t)
		t.Disconnect()
	}
}

func (ht *HeadTracker) updateBlockHeader() error {
	ht.mutex.Lock()
	defer ht.mutex.Unlock()

	header, err := ht.store.TxManager.GetBlockByNumber("latest")
	if err != nil {
		logger.Warnw("Unable to update latest block header", "err", err)
		return err
	}

	bn := header.ToIndexableBlockNumber()
	if bn.GreaterThan(ht.LastRecord()) {
		logger.Debug("Fast forwarding to block header ", presenters.FriendlyBigInt(bn.ToInt()))
		ht.number = bn
		return ht.store.Save(bn)
	}
	return nil
}

func (ht *HeadTracker) newHeadListener() (chan models.BlockHeader, models.EthSubscription, error) {
	headers := make(chan models.BlockHeader)
	sub, err := ht.store.TxManager.SubscribeToNewHeads(headers)
	return headers, sub, err
}

func (ht *HeadTracker) listenToNewHeads(headers chan models.BlockHeader, sub models.EthSubscription) {
	ht.listenToNewHeadsWaiter.Add(1)
	defer ht.listenToNewHeadsWaiter.Done()

	ht.sleeper.Reset()
	for {
		//fmt.Println("connectLoop...")
		err := ht.headLoop(headers, sub.Err())
		if err == nil {
			//fmt.Println("returning from connectLoop")
			return
		}

		//fmt.Println("reconnecting!")
		sub.Unsubscribe()

		ht.sleeper.Sleep()
		logger.Info("Reconnecting to node ", ht.store.Config.EthereumURL, " in ", ht.sleeper.Duration())

		headers, sub, err = ht.newHeadListener()
		if err != nil {
			logger.Warnw(fmt.Sprintf("Error reconnecting to %v", ht.store.Config.EthereumURL), "err", err)
		}
		logger.Info("Reconnected to node ", ht.store.Config.EthereumURL)
		//fmt.Println("reconnected!")
	}
}

func (ht *HeadTracker) headLoop(headers chan models.BlockHeader, errors <-chan error) error {
	ht.mutex.RLock()
	ht.connect(ht.number)
	ht.mutex.RUnlock()
	// FIXME: Deadlock with Stop here
	defer ht.disconnect()

	//defer fmt.Println("headLoop!")
	for {
		//fmt.Println("headLoop...")
		select {
		case header := <-headers:
			//fmt.Println("Got header", header)
			number := header.ToIndexableBlockNumber()
			logger.Debugw(fmt.Sprintf("Received header %v with hash %s", presenters.FriendlyBigInt(number.ToInt()), header.Hash().String()), "hash", header.Hash())
			if err := ht.Save(number); err != nil {
				logger.Error(err.Error())
			} else {
				ht.onNewHead(&header)
			}
		case err := <-errors:
			//fmt.Println("got errors")
			return err
		case <-ht.listenToNewHeadsCloser:
			//fmt.Println("got listenToNewHeadsCloser")
			return nil
		}
	}
}

func (ht *HeadTracker) onNewHead(head *models.BlockHeader) {
	ht.trackersMutex.RLock()
	defer ht.trackersMutex.RUnlock()
	for _, t := range ht.trackers {
		//fmt.Println("Sending onNeaHead signal to", t)
		t.OnNewHead(head)
	}
}
