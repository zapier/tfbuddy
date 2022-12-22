package runstream

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog/log"
)

const RunPollingStreamNameV0 = "RUN_POLLING"
const RunPollingKvName = "POLLING_TASKS"

var SuccessResponse = []byte("success")
var SuccessDoneResponse = []byte("done")
var FailedResponse = []byte("failed")

const TaskPollingDelayMinimum = 1 * time.Second
const TaskPollingDelayDefault = 10 * time.Second

type TFRunPollingTask struct {
	RunMetadata

	LastStatus string
	NextPoll   time.Time
	Processing bool
	LastUpdate time.Time

	// stream is the NATS Jetstream that this task is stored in
	stream *Stream

	// Revision is the NATS KV entry revision
	Revision uint64
}

func (s *Stream) NewTFRunPollingTask(meta RunMetadata, delay time.Duration) RunPollingTask {
	nextPoll := time.Now().Add(TaskPollingDelayDefault)
	if delay >= TaskPollingDelayMinimum {
		nextPoll = time.Now().Add(delay)
	}
	return &TFRunPollingTask{
		RunMetadata: meta,
		LastStatus:  "new",
		NextPoll:    nextPoll,
		Processing:  false,
		LastUpdate:  time.Now(),

		stream:   s,
		Revision: 0,
	}
}

func (task *TFRunPollingTask) Schedule() error {
	return task.stream.addTFRunPollingTask(task)
}

func (task *TFRunPollingTask) Reschedule() error {
	task.NextPoll = time.Now().Add(TaskPollingDelayDefault)
	task.Processing = false
	return task.update()
}

func (task *TFRunPollingTask) Completed() error {
	return task.stream.pollingKV.Delete(task.GetRunID())
}
func (task *TFRunPollingTask) GetRunID() string {
	return task.RunMetadata.GetRunID()
}
func (task *TFRunPollingTask) GetLastStatus() string {
	return task.LastStatus
}
func (task *TFRunPollingTask) GetRunMetaData() RunMetadata {
	return task.RunMetadata
}
func (task *TFRunPollingTask) SetLastStatus(status string) {
	task.LastStatus = status
}
func (task *TFRunPollingTask) update() error {
	task.LastUpdate = time.Now()
	b, _ := encodeTFRunPollingTask(task)
	rev, err := task.stream.pollingKV.Update(pollingKVKey(task), b, task.Revision)
	if err != nil {
		// TODO: are there are errors we need to handle?
		log.Debug().Err(err).Msg("failed to update polling task KV entry")
		return err
	}
	task.Revision = rev
	return nil
}

func (s *Stream) addTFRunPollingTask(task *TFRunPollingTask) error {
	b, err := encodeTFRunPollingTask(task)
	if err != nil {
		return err
	}
	task.Revision, err = s.pollingKV.Create(pollingKVKey(task), b)
	if err != nil {
		log.Error().Err(err).Msg("failed to add Polling Task to KV store")
	}

	return err
}

func (s *Stream) startPollingTaskDispatcher() {
	go func() {
		for {
			time.Sleep(time.Second)

			keys, err := s.pollingKV.Keys()
			if err != nil && err != nats.ErrNoKeysFound {
				log.Warn().Err(err).Msg("could not read polling tasks KV")
				continue
			}

			for _, key := range keys {
				entry, err := s.pollingKV.Get(key)
				if err != nil {
					log.Warn().Err(err).Str("key", key).Msg("could not read polling task from KV")
					continue
				}

				// check if ready
				task, err := s.decodeTFRunPollingTaskKVEntry(entry)
				if err != nil {
					// task is corrupted somehow, dump it
					log.Warn().Err(err).Str("key", key).Msg("deleting corrupt polling task")
					if err := s.pollingKV.Delete(key); err != nil {
						log.Error().Err(err).Msg("could not delete Polling Task from KV")
					}
					continue
				}
				if !task.Processing && time.Now().After(task.NextPoll) {
					// set & get processing status
					task.Processing = true
					err := task.update()
					if err == nil {
						// dispatch task and wait for response
						b, _ := encodeTFRunPollingTask(task)
						log.Debug().Str("runID", task.GetRunID()).Msg("enqueuing polling task")
						if _, err := s.js.PublishAsync(pollingStreamKey(task), b); err != nil {
							log.Error().Err(err).Msg("could not queue polling task")
							continue
						}

					}
				}

			}

		}

	}()
}

const pollingQueueName = "polling"

func (s *Stream) SubscribeTFRunPollingTasks(cb func(task RunPollingTask) bool) (closer func(), err error) {
	_, err = s.js.QueueSubscribe(
		fmt.Sprintf("%s.*", RunPollingStreamNameV0),
		pollingQueueName,
		func(msg *nats.Msg) {
			task, err := s.decodeTFRunPollingTask(msg.Data)
			if err != nil {
				if err := msg.Term(); err != nil {
					log.Error().Err(err).Msg("could not Terminate NATS msg")
				}
				return
			}
			log := log.With().
				Str("queue", "PollingTasks").
				Str("RunID", task.GetRunID()).
				Logger()

			if err := msg.InProgress(); err != nil {
				if err := msg.Nak(); err != nil {
					log.Error().Err(err).Msg("could not Nak NATS msg")
				}
				return
			}
			if cb(task) {
				if err := msg.Ack(); err != nil {
					log.Error().Err(err).Msg("could not Ack NATS msg")
				}
			} else {
				if err := msg.Nak(); err != nil {
					log.Error().Err(err).Msg("could not Nak NATS msg")
				}
			}
		},
	)

	closer = func() {
		// sub.Unsubscribe()
	}
	return closer, err
}

func (s *Stream) decodeTFRunPollingTaskKVEntry(entry nats.KeyValueEntry) (*TFRunPollingTask, error) {
	run := &TFRunPollingTask{}
	run.RunMetadata = &TFRunMetadata{}
	err := json.Unmarshal(entry.Value(), &run)
	if err != nil {
		log.Error().Err(err).Msg("unexpected error while decoding TF Run Polling Task KV entry")
	}

	// backwards compat
	// TODO: remove once upgraded
	if run.RunMetadata.GetRunID() == "" {
		err := json.Unmarshal(entry.Value(), run.RunMetadata)
		if err != nil {
			log.Error().Err(err).Msg("unexpected error while decoding TF Run Polling Task KV metadata")
		}
	}

	run.stream = s
	// update task revision with latest from Nats
	run.Revision = entry.Revision()
	return run, err
}

func (s *Stream) decodeTFRunPollingTask(b []byte) (*TFRunPollingTask, error) {
	run := &TFRunPollingTask{}
	run.RunMetadata = &TFRunMetadata{}
	err := json.Unmarshal(b, &run)
	run.stream = s

	return run, err
}

func encodeTFRunPollingTask(run *TFRunPollingTask) ([]byte, error) {
	return json.Marshal(run)
}

func pollingStreamKey(task *TFRunPollingTask) string {
	return fmt.Sprintf("%s.%s", RunPollingStreamNameV0, task.GetRunID())
}

func pollingKVKey(task *TFRunPollingTask) string {
	return task.GetRunID()
}

func configureRunPollingKVStore(js nats.JetStreamContext) (nats.KeyValue, error) {
	cfg := &nats.KeyValueConfig{
		Bucket:      RunPollingKvName,
		Description: "KV store for Polling Tasks",
		TTL:         time.Hour * 2,
		Storage:     nats.MemoryStorage,
		Replicas:    1,
	}

	for store := range js.KeyValueStores() {
		if store.Bucket() == cfg.Bucket {
			return js.KeyValue(cfg.Bucket)
		}
	}

	return js.CreateKeyValue(cfg)
}

func configureTFRunPollingTaskStream(js nats.JetStreamContext) {
	sCfg := &nats.StreamConfig{
		Name:        RunPollingStreamNameV0,
		Description: "Terraform Cloud Run Polling Tasks",
		Retention:   nats.WorkQueuePolicy,
		Subjects:    []string{fmt.Sprintf("%s.*", RunPollingStreamNameV0)},
		MaxMsgs:     1024,
		MaxAge:      time.Hour * 6,
		Replicas:    1,
	}

	addOrUpdateStream(js, sCfg)
}
