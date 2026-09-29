// Package messaging is a Kafka producer and consumer, and the delivery guarantee nobody has.
//
// # The one thing to understand
//
// A partition is an append-only log. A producer appends, a consumer reads forward and remembers how far it got.
// Everything else (ordering, parallelism, replay, consumer groups) falls out of that.
//
//	ORDERING is per PARTITION, never per topic. Messages with the same KEY go to the same
//	  partition, so ordering is guaranteed per key and not at all between keys. A topic with
//	  twelve partitions has twelve independent ordered logs.
//	PARALLELISM is capped by the partition count. Twenty consumers in a group on a twelve-partition
//	  topic leaves eight doing nothing, and adding consumers cannot help. Partition count is
//	  the scaling decision and it is made when the topic is created.
//	REPLAY is free, because nothing is deleted on read. A consumer group can reset its offset and
//	  read history, which is the property a queue does not have.
//
// # The delivery guarantees, honestly
//
//	AT MOST ONCE   commit the offset, then process. A crash between the two loses the message.
//	               Almost never what anyone wants and it is what the default settings of some
//	               clients give you.
//	AT LEAST ONCE  process, then commit. A crash between the two redelivers. This is the correct
//	               default and it means the consumer MUST be idempotent.
//	EXACTLY ONCE   does not exist end to end. Kafka transactions give exactly-once between Kafka
//	               topics, which is a real and narrow thing. The moment a consumer writes to a
//	               database or calls an API, the guarantee is at-least-once plus idempotency, and
//	               "exactly once" is a marketing claim about the part of the pipeline that is
//	               internal to Kafka.
//
// The practical consequence: idempotency is not an optimisation to add later. It is the thing that makes
// at-least-once usable, and it belongs in the consumer's design from the first line.
//
// # Why segmentio/kafka-go
//
// Two Go clients are in use. twmb/franz-go is faster, supports every protocol feature, and has an API shaped
// like Kafka's protocol. segmentio/kafka-go is slower, covers less, and has an API shaped like io.Reader and
// io.Writer, which is why it is here: a Reader you call ReadMessage on and a Writer you call WriteMessages on
// are obvious, and the interesting decisions are then visible as configuration rather than buried in a
// protocol client.
//
// For a service that needs transactions or the newest protocol features, franz-go is the answer.
package messaging

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/segmentio/kafka-go"
)

// Producer writes messages.
type Producer struct {
	writer *kafka.Writer

	written atomic.Int64
	failed  atomic.Int64
}

// ProducerConfig holds the settings that decide durability against throughput.
type ProducerConfig struct {
	Brokers []string
	Topic   string

	// RequiredAcks is the durability knob and the one that matters most.
	//
	//	RequireNone (0)  fire and forget. The write returns before the broker has it, so a
	//	                 broker restart loses messages and the producer never knows.
	//	RequireOne  (1)  the leader has it. Loses data if the leader dies before the followers
	//	                 replicate, which is a narrow window and a real one.
	//	RequireAll (-1)  every in-sync replica has it. The only setting that survives a broker
	//	                 failure, and the default here.
	//
	// kafka-go's zero value for this field is RequireNone, so a Writer built with an empty config
	// silently loses messages. That is the single most dangerous default in the library.
	RequiredAcks kafka.RequiredAcks

	// BatchSize and BatchTimeout trade latency for throughput. A batch of 100 with a 10ms timeout
	// means a message waits up to 10ms, and one network round trip carries a hundred messages
	// instead of one.
	BatchSize    int
	BatchTimeout time.Duration

	// Async makes WriteMessages return immediately. It also means errors are delivered to a
	// callback instead of being returned, so a service that sets Async and checks the error of
	// WriteMessages is checking nothing. See NewProducer.
	Async bool

	// Compression. Worth it for anything above a few hundred bytes: Kafka stores and transfers the
	// compressed batch, so it reduces disk, network and broker memory at once.
	Compression kafka.Compression
}

// DefaultProducerConfig is durable rather than fast.
func DefaultProducerConfig(brokers []string, topic string) ProducerConfig {
	return ProducerConfig{
		Brokers:      brokers,
		Topic:        topic,
		RequiredAcks: kafka.RequireAll,
		BatchSize:    100,
		BatchTimeout: 10 * time.Millisecond,
		Compression:  kafka.Snappy,
	}
}

// NewProducer builds one.
//
// The error callback is not optional when Async is set. With Async, WriteMessages returns nil before the write
// has happened, so the only place a failure can surface is the callback, and a nil callback means failures are
// silently discarded. A producer that reports success for messages that were never written is worse than one
// that is slow.
func NewProducer(cfg ProducerConfig, onError func(kafka.Message, error)) *Producer {
	p := &Producer{}

	p.writer = &kafka.Writer{
		Addr:  kafka.TCP(cfg.Brokers...),
		Topic: cfg.Topic,

		// Hash by key, which is what makes ordering per key work. The default is round robin,
		// which spreads messages evenly and destroys per-key ordering, and it is the right
		// default only for a topic where nothing has a key.
		Balancer: &kafka.Hash{},

		RequiredAcks: cfg.RequiredAcks,
		BatchSize:    cfg.BatchSize,
		BatchTimeout: cfg.BatchTimeout,
		Async:        cfg.Async,
		Compression:  cfg.Compression,

		// Create the topic if it is missing. Convenient for a test and a decision to make
		// deliberately in production: auto-created topics get the broker's default partition
		// count, which is usually 1, and a topic that silently has one partition cannot scale
		// and cannot be repartitioned without losing ordering.
		AllowAutoTopicCreation: true,
	}

	if cfg.Async {
		p.writer.Completion = func(messages []kafka.Message, err error) {
			if err != nil {
				p.failed.Add(int64(len(messages)))

				if onError != nil {
					for _, m := range messages {
						onError(m, err)
					}
				}

				return
			}

			p.written.Add(int64(len(messages)))
		}
	}

	return p
}

// Write sends messages.
func (p *Producer) Write(ctx context.Context, messages ...kafka.Message) error {
	if err := p.writer.WriteMessages(ctx, messages...); err != nil {
		p.failed.Add(int64(len(messages)))
		return fmt.Errorf("writing %d message(s): %w", len(messages), err)
	}

	if !p.writer.Async {
		p.written.Add(int64(len(messages)))
	}

	return nil
}

// Close flushes and closes.
//
// Not optional with Async: buffered messages are written by Close, and a process that exits without calling it
// loses whatever was in the batch. That is the async producer's real cost, and it is paid at shutdown rather
// than at write time.
func (p *Producer) Close() error {
	if err := p.writer.Close(); err != nil {
		return fmt.Errorf("closing the writer: %w", err)
	}
	return nil
}

// Stats reports what the producer counted.
func (p *Producer) Stats() (written, failed int64) {
	return p.written.Load(), p.failed.Load()
}

// CommitMode is the delivery guarantee, made a required parameter.
//
// A named type with no default, because this is the decision the package exists to make visible. A consumer
// library that picks one for you has made the most important choice in the system on your behalf.
type CommitMode int

const (
	// CommitAfterProcessing is at-least-once. Process, then commit. A crash between the two
	// redelivers, so the handler must be idempotent.
	CommitAfterProcessing CommitMode = iota

	// CommitBeforeProcessing is at-most-once. Commit, then process. A crash between the two loses
	// the message permanently, and nothing in the system records that it happened.
	CommitBeforeProcessing
)

// String names the mode.
func (m CommitMode) String() string {
	switch m {
	case CommitAfterProcessing:
		return "at-least-once (commit after)"
	case CommitBeforeProcessing:
		return "at-most-once (commit before)"
	default:
		return "unknown"
	}
}

// Consumer reads from a topic as part of a group.
type Consumer struct {
	reader *kafka.Reader
	mode   CommitMode

	processed atomic.Int64
	committed atomic.Int64
	failures  atomic.Int64
}

// ConsumerConfig holds the settings.
type ConsumerConfig struct {
	Brokers []string
	Topic   string

	// GroupID makes this a member of a consumer group: partitions are divided between members and
	// offsets are stored in Kafka. Without it, the reader reads a single partition and manages its
	// own offset, which is the right shape for a tail-the-log tool and wrong for a service.
	GroupID string

	// StartOffset is where a NEW group starts: kafka.FirstOffset for the whole history,
	// kafka.LastOffset for only what arrives from now.
	//
	// It applies only when the group has no committed offset. Changing it for an existing group
	// does nothing, which is why "we set it to FirstOffset and it did not replay" is a common
	// confusion.
	StartOffset int64

	// MinBytes and MaxBytes control how long a fetch waits. MinBytes of 1 fetches immediately;
	// a larger value waits for MaxWait to fill a batch, which is throughput against latency again.
	MinBytes int
	MaxBytes int
	MaxWait  time.Duration
}

// DefaultConsumerConfig is low-latency rather than high-throughput, which is right for a service and wrong for
// a batch job.
func DefaultConsumerConfig(brokers []string, topic, group string) ConsumerConfig {
	return ConsumerConfig{
		Brokers:     brokers,
		Topic:       topic,
		GroupID:     group,
		StartOffset: kafka.FirstOffset,
		MinBytes:    1,
		MaxBytes:    10 << 20,
		MaxWait:     250 * time.Millisecond,
	}
}

// NewConsumer builds one.
func NewConsumer(cfg ConsumerConfig, mode CommitMode) *Consumer {
	return &Consumer{
		mode: mode,
		reader: kafka.NewReader(kafka.ReaderConfig{
			Brokers:     cfg.Brokers,
			Topic:       cfg.Topic,
			GroupID:     cfg.GroupID,
			StartOffset: cfg.StartOffset,
			MinBytes:    cfg.MinBytes,
			MaxBytes:    cfg.MaxBytes,
			MaxWait:     cfg.MaxWait,

			// CommitInterval zero means commits are SYNCHRONOUS and explicit, which is what
			// makes the CommitMode above meaningful. A non-zero interval commits in the
			// background on a timer, so the guarantee becomes "at least once, with a window
			// of up to CommitInterval", and the code no longer controls it.
			CommitInterval: 0,
		}),
	}
}

// Run consumes until the context is cancelled.
//
// The loop is the whole subject. FetchMessage reads WITHOUT committing; CommitMessages commits. Separating them
// is what makes the guarantee a choice, and kafka-go's ReadMessage, which does both, is the convenient call
// that takes the choice away.
//
// # The commit does not use the loop's context
//
// It uses a fresh one with its own short timeout, and that is not fussiness. The loop's context is the shutdown
// signal: when it is cancelled, the work for the message in hand is finished and the offset still has to be
// written. Committing with the cancelled context fails with "context canceled", the offset is never stored, and
// the message is redelivered on the next start.
//
// So every graceful shutdown produces a duplicate. At-least-once says that is allowed, and producing one on
// every deploy for no reason is still a bug, and it is invisible until someone asks why the same order id
// appears twice in the logs every Tuesday afternoon.
//
// A test found this: four tests cancelled the context from inside their handler and every one failed with
// "committing after processing: context canceled".
func (c *Consumer) Run(ctx context.Context, handle func(context.Context, kafka.Message) error) error {
	for {
		m, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil
			}
			return fmt.Errorf("fetching: %w", err)
		}

		if c.mode == CommitBeforeProcessing {
			if err := c.commit(m); err != nil {
				return fmt.Errorf("committing before processing: %w", err)
			}
		}

		if err := handle(ctx, m); err != nil {
			c.failures.Add(1)

			// The message is NOT committed, so it will be redelivered. That is correct for a
			// transient failure and an infinite loop for a permanent one: a message that can
			// never be processed blocks the partition forever, because offsets are
			// sequential and there is no way to skip one.
			//
			// The answer is a dead letter topic, which DeadLetter below writes to. Without
			// one, a single malformed message stops a partition and the symptom is
			// "consumer lag on partition 7 only".
			return fmt.Errorf("handling message at offset %d: %w", m.Offset, err)
		}

		c.processed.Add(1)

		if c.mode == CommitAfterProcessing {
			if err := c.commit(m); err != nil {
				return fmt.Errorf("committing after processing: %w", err)
			}
		}
	}
}

// commit writes the offset with a context of its own.
//
// context.Background plus a timeout, never the loop's context. See Run.
//
// The timeout still matters: a commit that hangs during shutdown holds the process open past whatever the
// orchestrator allows, and then it is a SIGKILL rather than a clean stop.
func (c *Consumer) commit(m kafka.Message) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := c.reader.CommitMessages(ctx, m); err != nil {
		return err
	}

	c.committed.Add(1)

	return nil
}

// Close stops the consumer.
func (c *Consumer) Close() error {
	if err := c.reader.Close(); err != nil {
		return fmt.Errorf("closing the reader: %w", err)
	}
	return nil
}

// Stats reports what the consumer counted.
func (c *Consumer) Stats() (processed, committed, failures int64) {
	return c.processed.Load(), c.committed.Load(), c.failures.Load()
}

// Lag reports how far behind this consumer is.
//
// The number to alert on, and the one people build dashboards for. Rising lag means the consumers cannot keep
// up; lag on ONE partition means a poison message or a hot key, which is a different problem with the same
// symptom on a topic-level graph.
func (c *Consumer) Lag() int64 { return c.reader.Lag() }

// DeadLetter writes a message that could not be processed to another topic.
//
// # What has to be preserved
//
// The original topic, partition, offset and key, as headers. Without them the dead letter is a body with no
// provenance, and the whole point of a dead letter queue is being able to answer "where did this come from and
// what happened to it" six weeks later.
func DeadLetter(ctx context.Context, p *Producer, original kafka.Message, cause error) error {
	headers := append([]kafka.Header{}, original.Headers...)

	headers = append(headers,
		kafka.Header{Key: "dlq-topic", Value: []byte(original.Topic)},
		kafka.Header{Key: "dlq-partition", Value: []byte(strconv.Itoa(original.Partition))},
		kafka.Header{Key: "dlq-offset", Value: []byte(strconv.FormatInt(original.Offset, 10))},
		kafka.Header{Key: "dlq-error", Value: []byte(cause.Error())},
		kafka.Header{Key: "dlq-at", Value: []byte(time.Now().UTC().Format(time.RFC3339))},
	)

	return p.Write(ctx, kafka.Message{
		// The SAME key, so a dead letter lands on the same partition as its siblings and the
		// per-key ordering of the failures is preserved. It matters for replaying them.
		Key:     original.Key,
		Value:   original.Value,
		Headers: headers,
	})
}

// Idempotent is the consumer-side half of at-least-once.
//
// # Why this is here and not in the caller
//
// Because at-least-once is not a guarantee you get, it is one you complete. Kafka promises redelivery; the
// consumer has to promise that redelivery is harmless. Without that half, at-least-once means "duplicates
// reach production".
//
// This is the same shape as the webhook Deduper, with the same caveat: an in-memory map is wrong for more than
// one replica, and the real implementation is a unique constraint in the same transaction as the work.
type Idempotent struct {
	mu   sync.Mutex
	seen map[string]struct{}

	duplicates atomic.Int64
}

// NewIdempotent builds one.
func NewIdempotent() *Idempotent {
	return &Idempotent{seen: make(map[string]struct{})}
}

// Wrap returns a handler that skips messages it has already processed.
//
// The id comes from the MESSAGE, not from the offset: an offset identifies a position in a partition, and the
// same logical event redelivered after a producer retry has a different offset. A business id in the payload or
// a header is the only thing that survives.
func (i *Idempotent) Wrap(id func(kafka.Message) string, handle func(context.Context, kafka.Message) error) func(context.Context, kafka.Message) error {
	return func(ctx context.Context, m kafka.Message) error {
		key := id(m)

		if key == "" {
			// No id means no deduplication is possible. Processing it is the only option and
			// saying so is better than silently treating "" as a key that every message
			// shares, which would drop everything after the first.
			return handle(ctx, m)
		}

		// Reserve the key before the work, so a duplicate delivered while this one is still running
		// is skipped rather than processed twice at once.
		i.mu.Lock()
		_, already := i.seen[key]
		if !already {
			i.seen[key] = struct{}{}
		}
		i.mu.Unlock()

		if already {
			i.duplicates.Add(1)
			return nil
		}

		// And release it if the work fails. Without this, the first failure marks the message as done,
		// Kafka redelivers it as promised, and the redelivery is skipped as a duplicate of work that
		// never happened: at-most-once with an at-least-once label. The first version of Wrap had
		// exactly that bug, and TestIdempotentRetriesAFailedMessage is the regression test.
		//
		// A duplicate skipped while the first attempt was in flight is not lost when that attempt
		// fails, because the failed attempt is itself redelivered.
		//
		// The durable version has no window at all: the id goes in with a unique constraint in the
		// SAME transaction as the work, so both commit or neither does.
		if err := handle(ctx, m); err != nil {
			i.mu.Lock()
			delete(i.seen, key)
			i.mu.Unlock()

			return err
		}

		return nil
	}
}

// Duplicates reports how many were skipped.
func (i *Idempotent) Duplicates() int64 { return i.duplicates.Load() }
