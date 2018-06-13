package services

//
// JobSubscriber includes a worker pool for executing JobRuns concurrently on
// receipt of a new block head.
//
// ** This is a quick and dirty hack **
//
// This means tha JobRuns that include a Sleep adapter to not block other
// JobRuns from executing.This is done with a pool of workers (1 per JobRun),
// with each one given a channel to receive new BlockNumbers.
//
// The downsides of this are:
//  1) number of go routines = number of jobs
//  2) lots of sleeping jobs can cause the channels to expand without bounds
//  and could lead to an OOM error
//

import (
	"sync"
	"time"

	"github.com/smartcontractkit/chainlink/logger"
	"github.com/smartcontractkit/chainlink/store"
	"github.com/smartcontractkit/chainlink/store/models"
	"go.uber.org/multierr"
)

// JobSubscriber listens for push notifications from the ethereum node's
// websocket for specific jobs.
type JobSubscriber interface {
	AddJob(job models.JobSpec, bn *models.IndexableBlockNumber) error
	Connect(bn *models.IndexableBlockNumber) error
	Disconnect()
	Jobs() []models.JobSpec
	OnNewHead(head *models.BlockHeader)
	Stop()
}

// jobSubscriber implementation
type jobSubscriber struct {
	Store            *store.Store
	jobSubscriptions []JobSubscription
	jobsMutex        sync.Mutex
	workerMutex      sync.Mutex
	workers          map[string]chan *models.IndexableBlockNumber
	workerWaiter     sync.WaitGroup
}

// NewJobSubscriber returns a new job subscriber.
func NewJobSubscriber(store *store.Store) JobSubscriber {
	return &jobSubscriber{
		Store:   store,
		workers: make(map[string]chan *models.IndexableBlockNumber),
	}
}

// AddJob subscribes to ethereum log events for each "runlog" and "ethlog"
// initiator in the passed job spec.
func (js *jobSubscriber) AddJob(job models.JobSpec, bn *models.IndexableBlockNumber) error {
	if !job.IsLogInitiated() {
		return nil
	}

	sub, err := StartJobSubscription(job, bn, js.Store)
	if err != nil {
		return err
	}
	js.addSubscription(sub)
	return nil
}

// Jobs returns the jobs being listened to.
func (js *jobSubscriber) Jobs() []models.JobSpec {
	var jobs []models.JobSpec
	for _, js := range js.jobSubscriptions {
		jobs = append(jobs, js.Job)
	}
	return jobs
}

func (js *jobSubscriber) addSubscription(sub JobSubscription) {
	js.jobsMutex.Lock()
	defer js.jobsMutex.Unlock()
	js.jobSubscriptions = append(js.jobSubscriptions, sub)
}

// Connect connects the jobs to the ethereum node by creating corresponding subscriptions.
func (js *jobSubscriber) Connect(bn *models.IndexableBlockNumber) error {
	jobs, err := js.Store.Jobs()
	if err != nil {
		return err
	}
	for _, j := range jobs {
		err = multierr.Append(err, js.AddJob(j, bn))
	}
	return err
}

// Disconnect disconnects all subscriptions associated with jobs belonging to
// this listener.
func (js *jobSubscriber) Disconnect() {
	js.jobsMutex.Lock()
	defer js.jobsMutex.Unlock()
	for _, sub := range js.jobSubscriptions {
		sub.Unsubscribe()
	}
	js.jobSubscriptions = []JobSubscription{}
}

// OnNewHead resumes all pending job runs based on the new head activity.
func (js *jobSubscriber) OnNewHead(head *models.BlockHeader) {
	pendingRuns, err := js.Store.JobRunsWithStatus(models.RunStatusPendingConfirmations)
	if err != nil {
		logger.Error(err.Error())
	}

	activeJobRunIDs := make(map[string]struct{})

	js.workerMutex.Lock()
	defer js.workerMutex.Unlock()
	for _, jr := range pendingRuns {
		activeJobRunIDs[jr.ID] = struct{}{}

		workerChannel, ok := js.workers[jr.ID]
		if !ok {
			workerChannel = make(chan *models.IndexableBlockNumber)
			js.workers[jr.ID] = workerChannel

			go func() {
				js.workerWaiter.Add(1)

				for blockNumber := range workerChannel {
					if blockNumber == nil {
						logger.Debug("Stopped worker for", jr.ID)
						break
					}

					logger.Debug("Woke up", jr.ID, "worker to process", blockNumber)
					// TODO: Backfilling of missed block numbers on "power loss" situation
					// ExecuteRun.. UpToBlock ?
					if _, err := ExecuteRunAtBlock(jr, js.Store, jr.Result, blockNumber); err != nil {
						logger.Error(err.Error())
					}
				}

				js.workerWaiter.Done()
			}()
		}

		blockNumber := head.ToIndexableBlockNumber()
		workerChannel <- blockNumber
	}

	// Stop any workers that didn't have corresponding pending confirmations
	for id, workerChannel := range js.workers {
		if _, ok := activeJobRunIDs[id]; !ok {
			workerChannel <- nil
			delete(js.workers, id)
		}
	}
}

// waitTimeout waits for a waitGroup to be finished, but abandons it after a
// specified timeout.
//
// This is used to allow the chainlink node to finish within a reasoble time
// interval, even if a worker thread is procesing a Sleep adaptor with a long
// duration.
func waitTimeout(wg *sync.WaitGroup, timeout time.Duration) bool {
	completion := make(chan struct{})
	go func() {
		defer close(completion)
		wg.Wait()
	}()
	select {
	case <-completion:
		return false
	case <-time.After(timeout):
		return true
	}
}

// Stop closes all workers that have been started to process Job Runs on new heads and waits for them to finish.
func (js *jobSubscriber) Stop() {
	js.workerMutex.Lock()
	for _, workerChannel := range js.workers {
		workerChannel <- nil
	}
	js.workerMutex.Unlock()
	waitTimeout(&js.workerWaiter, 10*time.Second)
}
