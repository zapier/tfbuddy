package runstream

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/pkg/jsqueue"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
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
	ctx      context.Context
	Carrier  propagation.MapCarrier `json:"Carrier"`
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

func (task *TFRunPollingTask) Schedule(ctx context.Context) error {
	return task.stream.addTFRunPollingTask(ctx, task)
}

func (task *TFRunPollingTask) Reschedule(ctx context.Context) error {
	task.NextPoll = time.Now().Add(TaskPollingDelayDefault)
	task.Processing = false
	return task.update(ctx)
}

func (task *TFRunPollingTask) Completed() error {
	return task.stream.pollingKV.Delete(task.GetRunID())
}
func (task *TFRunPollingTask) GetRunID() string {
	return task.RunMetadata.GetRunID()
}
func (task *TFRunPollingTask) GetContext() context.Context {
	return task.ctx
}
func (task *TFRunPollingTask) SetCarrier(carrier map[string]string) {
	task.Carrier = carrier
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
func (task *TFRunPollingTask) update(ctx context.Context) error {
	ctx, span := otel.Tracer("terraform").Start(ctx, "update")
	defer span.End()

	task.LastUpdate = time.Now()
	span.SetAttributes(
		attribute.String("runID", task.GetRunID()),
	)

	b, _ := encodeTFRunPollingTask(ctx, task)
	rev, err := task.stream.pollingKV.Update(pollingKVKey(task), b, task.Revision)
	if err != nil {
		// TODO: are there are errors we need to handle?
		log.Debug().Err(err).Msg("failed to update polling task KV entry")
		return err
	}
	task.Revision = rev
	return nil
}

func (s *Stream) addTFRunPollingTask(ctx context.Context, task *TFRunPollingTask) error {
	ctx, span := otel.Tracer("terraform").Start(ctx, "addTFRunPollingTask")
	defer span.End()

	b, err := encodeTFRunPollingTask(ctx, task)
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
					ctx, span := otel.Tracer("terraform").Start(task.GetContext(), "polling")
					// set & get processing status
					task.Processing = true
					err := task.update(ctx)
					if err == nil {
						// dispatch task and wait for response
						b, _ := encodeTFRunPollingTask(ctx, task)
						log.Debug().Str("runID", task.GetRunID()).Msg("enqueuing polling task")
						if _, err := s.js.PublishAsync(pollingStreamKey(task), b); err != nil {
							span.RecordError(err)
							log.Error().Err(err).Msg("could not queue polling task")
							continue
						}
					}
					span.End()
				}

			}

		}

	}()
}

// pollingQueue is the pull consumer that delivers polling tasks. Older
// releases read them through a push consumer named "polling", which a
// work-queue stream does not allow alongside this one.
var pollingQueue = jsqueue.Queue{
	Stream:            RunPollingStreamNameV0,
	Subject:           fmt.Sprintf("%s.*", RunPollingStreamNameV0),
	Durable:           "run_polling_pull_worker",
	LegacyPushDurable: "polling",
}

// pollingTaskMessage decodes a RUN_POLLING message for jsqueue.
type pollingTaskMessage struct {
	task *TFRunPollingTask
}

func (m *pollingTaskMessage) DecodeEventData(b []byte) error {
	task, err := decodeTFRunPollingTaskData(b)
	m.task = task
	return err
}

// SubscribeTFRunPollingTasks hands polling tasks to cb, one at a time per
// replica. When cb fails to poll a run (it returns false), the task goes back
// to the dispatcher, which polls the run again after the usual delay.
func (s *Stream) SubscribeTFRunPollingTasks(cb func(task RunPollingTask) bool) (closer func(), err error) {
	opts := jsqueue.Options{NakDelay: TaskPollingDelayDefault}
	cc, err := jsqueue.Consume[pollingTaskMessage](s.jsc, pollingQueue, opts, func(m *pollingTaskMessage) error {
		task := m.task
		task.stream = s
		if cb(task) {
			return nil
		}
		// Redelivering the message right away, as older releases did, polls
		// TFC again immediately and indefinitely while it is failing.
		if err := task.Reschedule(task.GetContext()); err != nil {
			return fmt.Errorf("could not reschedule the polling task for run %s: %w", task.GetRunID(), err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return cc.Stop, nil
}

func (s *Stream) decodeTFRunPollingTaskKVEntry(entry nats.KeyValueEntry) (*TFRunPollingTask, error) {
	run := &TFRunPollingTask{}
	run.RunMetadata = &TFRunMetadata{}
	err := json.Unmarshal(entry.Value(), &run)
	if err != nil {
		log.Error().Err(err).Msg("unexpected error while decoding TF Run Polling Task KV entry")
	}

	run.ctx = otel.GetTextMapPropagator().Extract(context.Background(), run.Carrier)

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

func decodeTFRunPollingTaskData(b []byte) (*TFRunPollingTask, error) {
	run := &TFRunPollingTask{}
	run.RunMetadata = &TFRunMetadata{}
	err := json.Unmarshal(b, &run)

	ctx := context.Background()
	run.ctx = otel.GetTextMapPropagator().Extract(ctx, run.Carrier)

	return run, err
}

func encodeTFRunPollingTask(ctx context.Context, run *TFRunPollingTask) ([]byte, error) {
	run.Carrier = make(map[string]string)
	otel.GetTextMapPropagator().Inject(ctx, run.Carrier)
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
