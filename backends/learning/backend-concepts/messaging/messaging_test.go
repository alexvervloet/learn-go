package messaging

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"

	"github.com/alexvervloet/learn-go/backends/learning/backend-concepts/internal/kafkatest"
)

// TestOrderingIsPerKeyNotPerTopic is the property everything else rests on.
func TestOrderingIsPerKeyNotPerTopic(t *testing.T) {
	brokers := kafkatest.Require(t)
	topic := kafkatest.Topic(t, 4)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	p := NewProducer(DefaultProducerConfig(brokers, topic), nil)
	defer func() { _ = p.Close() }()

	// Three keys, ten messages each, interleaved. A per-key order that survives is the guarantee;
	// a global order is not promised and will not hold.
	const perKey = 10

	keys := []string{"alice", "bob", "carol"}

	var messages []kafka.Message

	for i := range perKey {
		for _, key := range keys {
			messages = append(messages, kafka.Message{
				Key:   []byte(key),
				Value: []byte(strconv.Itoa(i)),
			})
		}
	}

	if err := p.Write(ctx, messages...); err != nil {
		t.Fatal(err)
	}

	// Read everything back from one consumer, so the order seen is the order Kafka produced.
	c := NewConsumer(DefaultConsumerConfig(brokers, topic, kafkatest.Group(t)), CommitAfterProcessing)
	defer func() { _ = c.Close() }()

	var (
		mu        sync.Mutex
		byKey     = map[string][]int{}
		partition = map[string]int{}
		total     int
	)

	readCtx, readCancel := context.WithTimeout(ctx, 30*time.Second)
	defer readCancel()

	err := c.Run(readCtx, func(_ context.Context, m kafka.Message) error {
		n, err := strconv.Atoi(string(m.Value))
		if err != nil {
			return err
		}

		mu.Lock()
		key := string(m.Key)
		byKey[key] = append(byKey[key], n)
		partition[key] = m.Partition
		total++
		done := total == len(messages)
		mu.Unlock()

		if done {
			readCancel()
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()

	if total != len(messages) {
		t.Fatalf("read %d of %d messages", total, len(messages))
	}

	for key, values := range byKey {
		t.Logf("%-6s partition %d, values %v", key, partition[key], values)

		if len(values) != perKey {
			t.Errorf("%s: %d messages, want %d", key, len(values), perKey)
		}

		for i, v := range values {
			if v != i {
				t.Errorf("%s: value %d at position %d, so the per-key order is broken",
					key, v, i)
			}
		}
	}

	t.Log("every key is in order within itself. The messages were written interleaved and the " +
		"global order is not preserved, which is not a bug: ordering is per partition, and a " +
		"key picks a partition.")
}

// TestRoundRobinDestroysOrdering is the same test with the default balancer, which is what a Writer built
// without one gets.
func TestRoundRobinDestroysOrdering(t *testing.T) {
	brokers := kafkatest.Require(t)
	topic := kafkatest.Topic(t, 4)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// A raw Writer with no Balancer: kafka-go defaults to round robin.
	//
	// AllowAutoTopicCreation is set even though kafkatest.Topic already created the topic. Without
	// it this test fails intermittently with "Unknown Topic Or Partition": topic metadata
	// propagates asynchronously and a Writer's first request can reach a broker that has not caught
	// up. The flag makes that request a no-op refresh instead of an error, which is also why it is
	// on by default in this package's producer.
	w := &kafka.Writer{
		Addr:                   kafka.TCP(brokers...),
		Topic:                  topic,
		RequiredAcks:           kafka.RequireAll,
		BatchSize:              1,
		AllowAutoTopicCreation: true,
	}
	defer func() { _ = w.Close() }()

	const n = 20

	for i := range n {
		if err := w.WriteMessages(ctx, kafka.Message{
			Key:   []byte("one-key"),
			Value: []byte(strconv.Itoa(i)),
		}); err != nil {
			t.Fatal(err)
		}
	}

	c := NewConsumer(DefaultConsumerConfig(brokers, topic, kafkatest.Group(t)), CommitAfterProcessing)
	defer func() { _ = c.Close() }()

	var (
		mu         sync.Mutex
		partitions = map[int]bool{}
		read       int
	)

	readCtx, readCancel := context.WithTimeout(ctx, 30*time.Second)
	defer readCancel()

	if err := c.Run(readCtx, func(_ context.Context, m kafka.Message) error {
		mu.Lock()
		partitions[m.Partition] = true
		read++
		done := read == n
		mu.Unlock()

		if done {
			readCancel()
		}

		return nil
	}); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	spread := len(partitions)
	mu.Unlock()

	t.Logf("%d messages with the SAME key were spread over %d partitions", n, spread)

	if spread == 1 {
		t.Error("round robin put everything on one partition, so this demonstration is broken")
	}

	t.Log("kafka-go's default Balancer is round robin, so a Writer built without one spreads " +
		"a single key across every partition and per-key ordering is gone. The Hash balancer " +
		"is what makes the key mean anything.")
}

// TestAtLeastOnceRedelivers is the guarantee, demonstrated by crashing between processing and committing.
func TestAtLeastOnceRedelivers(t *testing.T) {
	brokers := kafkatest.Require(t)
	topic := kafkatest.Topic(t, 1)
	group := kafkatest.Group(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	p := NewProducer(DefaultProducerConfig(brokers, topic), nil)

	const n = 5

	for i := range n {
		if err := p.Write(ctx, kafka.Message{
			Key:   []byte("k"),
			Value: []byte(strconv.Itoa(i)),
		}); err != nil {
			t.Fatal(err)
		}
	}
	_ = p.Close()

	// First consumer: processes three messages, then "crashes" without committing the third.
	var firstRun []string

	first := NewConsumer(DefaultConsumerConfig(brokers, topic, group), CommitAfterProcessing)

	crashCtx, crash := context.WithTimeout(ctx, 30*time.Second)

	err := first.Run(crashCtx, func(_ context.Context, m kafka.Message) error {
		firstRun = append(firstRun, string(m.Value))

		if len(firstRun) == 3 {
			// The crash: return an error, which Run propagates WITHOUT committing.
			return errors.New("process died")
		}

		return nil
	})

	crash()

	if err == nil {
		t.Fatal("the handler's error did not propagate")
	}

	processed, committed, _ := first.Stats()
	_ = first.Close()

	t.Logf("first run processed %v, committed %d", firstRun, committed)

	if processed != 2 {
		t.Errorf("processed %d before the crash, want 2 committed successes", processed)
	}

	// Second consumer, same group: resumes from the last COMMITTED offset, so it sees the third
	// message again.
	var secondRun []string

	second := NewConsumer(DefaultConsumerConfig(brokers, topic, group), CommitAfterProcessing)
	defer func() { _ = second.Close() }()

	readCtx, readCancel := context.WithTimeout(ctx, 30*time.Second)
	defer readCancel()

	if err := second.Run(readCtx, func(_ context.Context, m kafka.Message) error {
		secondRun = append(secondRun, string(m.Value))

		if len(secondRun) == n-2 {
			readCancel()
		}

		return nil
	}); err != nil {
		t.Fatal(err)
	}

	t.Logf("second run read %v", secondRun)

	if len(secondRun) == 0 {
		t.Fatal("the second consumer read nothing")
	}

	// The message that was processed but not committed is delivered again.
	if secondRun[0] != firstRun[len(firstRun)-1] {
		t.Errorf("the second run started at %q, want a redelivery of %q",
			secondRun[0], firstRun[len(firstRun)-1])
	}

	t.Logf("message %q was processed twice, which is what at-least-once means and why the "+
		"handler has to be idempotent", secondRun[0])
}

// TestAtMostOnceLoses is the other mode, and the reason it is almost never wanted.
func TestAtMostOnceLoses(t *testing.T) {
	brokers := kafkatest.Require(t)
	topic := kafkatest.Topic(t, 1)
	group := kafkatest.Group(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	p := NewProducer(DefaultProducerConfig(brokers, topic), nil)

	const n = 5

	for i := range n {
		if err := p.Write(ctx, kafka.Message{
			Key:   []byte("k"),
			Value: []byte(strconv.Itoa(i)),
		}); err != nil {
			t.Fatal(err)
		}
	}
	_ = p.Close()

	var firstRun []string

	first := NewConsumer(DefaultConsumerConfig(brokers, topic, group), CommitBeforeProcessing)

	crashCtx, crash := context.WithTimeout(ctx, 30*time.Second)

	err := first.Run(crashCtx, func(_ context.Context, m kafka.Message) error {
		if len(firstRun) == 2 {
			// Committed already, and now the process dies before doing the work.
			return errors.New("process died after the commit")
		}

		firstRun = append(firstRun, string(m.Value))

		return nil
	})

	crash()

	if err == nil {
		t.Fatal("the handler's error did not propagate")
	}

	_ = first.Close()

	t.Logf("first run completed %v before dying", firstRun)

	var secondRun []string

	second := NewConsumer(DefaultConsumerConfig(brokers, topic, group), CommitBeforeProcessing)
	defer func() { _ = second.Close() }()

	readCtx, readCancel := context.WithTimeout(ctx, 20*time.Second)
	defer readCancel()

	_ = second.Run(readCtx, func(_ context.Context, m kafka.Message) error {
		secondRun = append(secondRun, string(m.Value))

		if len(secondRun) == n-3 {
			readCancel()
		}

		return nil
	})

	t.Logf("second run read %v", secondRun)

	completed := map[string]bool{}
	for _, v := range append(append([]string{}, firstRun...), secondRun...) {
		completed[v] = true
	}

	var lost []string
	for i := range n {
		if !completed[strconv.Itoa(i)] {
			lost = append(lost, strconv.Itoa(i))
		}
	}

	t.Logf("messages never processed by anything: %v", lost)

	if len(lost) == 0 {
		t.Error("nothing was lost, so this demonstration is not showing at-most-once")
	}

	t.Log("the message was committed and then never processed. Nothing in the system records " +
		"that it happened: no error, no lag, no dead letter. It is simply gone.")
}

// TestIdempotencyCompletesAtLeastOnce.
func TestIdempotencyCompletesAtLeastOnce(t *testing.T) {
	brokers := kafkatest.Require(t)
	topic := kafkatest.Topic(t, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	p := NewProducer(DefaultProducerConfig(brokers, topic), nil)
	defer func() { _ = p.Close() }()

	// The same logical event, written three times, as a producer retry would.
	for range 3 {
		if err := p.Write(ctx, kafka.Message{
			Key:   []byte("order-1"),
			Value: []byte(`{"event_id":"evt_abc","amount":1999}`),
			Headers: []kafka.Header{
				{Key: "event-id", Value: []byte("evt_abc")},
			},
		}); err != nil {
			t.Fatal(err)
		}
	}

	c := NewConsumer(DefaultConsumerConfig(brokers, topic, kafkatest.Group(t)), CommitAfterProcessing)
	defer func() { _ = c.Close() }()

	idempotent := NewIdempotent()

	var charged int

	readCtx, readCancel := context.WithTimeout(ctx, 30*time.Second)
	defer readCancel()

	var read int

	handler := idempotent.Wrap(
		func(m kafka.Message) string {
			for _, h := range m.Headers {
				if h.Key == "event-id" {
					return string(h.Value)
				}
			}
			return ""
		},
		func(context.Context, kafka.Message) error {
			charged++
			return nil
		})

	if err := c.Run(readCtx, func(ctx context.Context, m kafka.Message) error {
		read++

		if err := handler(ctx, m); err != nil {
			return err
		}

		if read == 3 {
			readCancel()
		}

		return nil
	}); err != nil {
		t.Fatal(err)
	}

	t.Logf("%d deliveries, %d charges, %d skipped as duplicates",
		read, charged, idempotent.Duplicates())

	if read != 3 {
		t.Errorf("read %d messages, want 3", read)
	}
	if charged != 1 {
		t.Errorf("charged %d times, want 1", charged)
	}
	if idempotent.Duplicates() != 2 {
		t.Errorf("%d duplicates recorded, want 2", idempotent.Duplicates())
	}

	t.Log("the id comes from the message, not from the offset: three deliveries of one event " +
		"have three different offsets and one event id")
}

// TestPartitionsCapParallelism is the scaling decision made at topic creation.
func TestPartitionsCapParallelism(t *testing.T) {
	brokers := kafkatest.Require(t)

	const partitions = 2

	topic := kafkatest.Topic(t, partitions)
	group := kafkatest.Group(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	p := NewProducer(DefaultProducerConfig(brokers, topic), nil)

	const n = 40

	for i := range n {
		if err := p.Write(ctx, kafka.Message{
			Key:   []byte(fmt.Sprintf("key-%d", i)),
			Value: []byte(strconv.Itoa(i)),
		}); err != nil {
			t.Fatal(err)
		}
	}
	_ = p.Close()

	// FOUR consumers in the group, on TWO partitions. Two of them will get nothing.
	const consumers = 4

	var (
		mu          sync.Mutex
		perConsumer = make([]int, consumers)
		total       int
		wg          sync.WaitGroup
	)

	readCtx, readCancel := context.WithTimeout(ctx, 45*time.Second)
	defer readCancel()

	for i := range consumers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			c := NewConsumer(DefaultConsumerConfig(brokers, topic, group), CommitAfterProcessing)
			defer func() { _ = c.Close() }()

			_ = c.Run(readCtx, func(context.Context, kafka.Message) error {
				mu.Lock()
				perConsumer[i]++
				total++
				done := total == n
				mu.Unlock()

				if done {
					readCancel()
				}

				return nil
			})
		}()
	}

	wg.Wait()

	mu.Lock()
	defer mu.Unlock()

	working := 0
	for i, count := range perConsumer {
		t.Logf("consumer %d read %d messages", i, count)

		if count > 0 {
			working++
		}
	}

	t.Logf("%d consumers on %d partitions: %d did any work at all", consumers, partitions, working)

	if total != n {
		t.Errorf("read %d of %d messages", total, n)
	}
	if working > partitions {
		t.Errorf("%d consumers did work on %d partitions, which cannot happen",
			working, partitions)
	}

	t.Log("adding consumers past the partition count changes nothing. Partition count is the " +
		"scaling decision and it is made when the topic is created; raising it later " +
		"re-hashes the keys and breaks the per-key ordering of everything already written.")
}

// TestDeadLetterPreservesProvenance.
func TestDeadLetterPreservesProvenance(t *testing.T) {
	brokers := kafkatest.Require(t)

	main := kafkatest.Topic(t, 1)
	dlq := kafkatest.Topic(t, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	p := NewProducer(DefaultProducerConfig(brokers, main), nil)

	if err := p.Write(ctx, kafka.Message{
		Key:     []byte("order-7"),
		Value:   []byte(`{"malformed`),
		Headers: []kafka.Header{{Key: "trace-id", Value: []byte("abc123")}},
	}); err != nil {
		t.Fatal(err)
	}
	_ = p.Close()

	dlqProducer := NewProducer(DefaultProducerConfig(brokers, dlq), nil)
	defer func() { _ = dlqProducer.Close() }()

	c := NewConsumer(DefaultConsumerConfig(brokers, main, kafkatest.Group(t)), CommitAfterProcessing)
	defer func() { _ = c.Close() }()

	readCtx, readCancel := context.WithTimeout(ctx, 30*time.Second)
	defer readCancel()

	if err := c.Run(readCtx, func(ctx context.Context, m kafka.Message) error {
		// A permanent failure. Without the dead letter, returning the error here blocks the
		// partition forever.
		cause := errors.New("invalid JSON")

		if err := DeadLetter(ctx, dlqProducer, m, cause); err != nil {
			return err
		}

		readCancel()

		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Read it back.
	dlqConsumer := NewConsumer(DefaultConsumerConfig(brokers, dlq, kafkatest.Group(t)), CommitAfterProcessing)
	defer func() { _ = dlqConsumer.Close() }()

	dlqCtx, dlqCancel := context.WithTimeout(ctx, 30*time.Second)
	defer dlqCancel()

	var dead kafka.Message

	if err := dlqConsumer.Run(dlqCtx, func(_ context.Context, m kafka.Message) error {
		dead = m
		dlqCancel()
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	headers := map[string]string{}
	for _, h := range dead.Headers {
		headers[h.Key] = string(h.Value)
	}

	t.Logf("dead letter key %q, value %q", dead.Key, dead.Value)
	for _, k := range []string{"trace-id", "dlq-topic", "dlq-partition", "dlq-offset", "dlq-error"} {
		t.Logf("  %-14s %s", k, headers[k])
	}

	if string(dead.Key) != "order-7" {
		t.Errorf("the key was not preserved: %q", dead.Key)
	}
	if headers["trace-id"] != "abc123" {
		t.Error("the original headers were not preserved")
	}
	if headers["dlq-topic"] != main {
		t.Errorf("dlq-topic is %q, want %q", headers["dlq-topic"], main)
	}
	if headers["dlq-error"] != "invalid JSON" {
		t.Errorf("dlq-error is %q", headers["dlq-error"])
	}

	t.Log("without the provenance headers a dead letter is a body with no history, and the " +
		"whole point is answering 'where did this come from' six weeks later")
}

// TestAsyncProducerNeedsTheCallback is the dangerous convenience.
func TestAsyncProducerNeedsTheCallback(t *testing.T) {
	brokers := kafkatest.Require(t)
	topic := kafkatest.Topic(t, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg := DefaultProducerConfig(brokers, topic)
	cfg.Async = true

	var (
		mu     sync.Mutex
		errsCh []error
	)

	p := NewProducer(cfg, func(_ kafka.Message, err error) {
		mu.Lock()
		errsCh = append(errsCh, err)
		mu.Unlock()
	})

	// The write returns nil before anything has happened.
	err := p.Write(ctx, kafka.Message{Key: []byte("k"), Value: []byte("v")})

	written, failed := p.Stats()

	t.Logf("Write returned %v, and at that moment %d written, %d failed", err, written, failed)

	if err != nil {
		t.Errorf("an async write returned an error: %v", err)
	}

	// Close flushes. Without it, buffered messages are lost at exit.
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}

	written, failed = p.Stats()

	t.Logf("after Close: %d written, %d failed", written, failed)

	if written != 1 {
		t.Errorf("%d written after Close, want 1", written)
	}

	t.Log("Write returned nil before the message existed anywhere. A service that checks that " +
		"error is checking nothing, and one that exits without Close loses the batch.")
}

// TestRequiredAcksDefaultIsFireAndForget, which is kafka-go's zero value.
func TestRequiredAcksDefaultIsFireAndForget(t *testing.T) {
	// A Writer with an empty config, which is what a first implementation looks like.
	w := &kafka.Writer{}

	t.Logf("kafka.Writer{}.RequiredAcks is %v (%d)", w.RequiredAcks, int(w.RequiredAcks))

	if w.RequiredAcks != kafka.RequireNone {
		t.Errorf("the zero value is %v; this lesson may be out of date", w.RequiredAcks)
	}

	t.Log("RequireNone means the write returns before the broker has the message, so a broker " +
		"restart loses it and the producer never finds out. It is the zero value, so it is " +
		"what you get by not deciding.")

	if DefaultProducerConfig(nil, "").RequiredAcks != kafka.RequireAll {
		t.Error("this package's default is not RequireAll")
	}
}

// TestIdempotentRetriesAFailedMessage needs no broker, which is why it is not skipped with the others.
//
// A deduper that records the id BEFORE the work turns a failure into a loss: the first delivery fails, Kafka
// redelivers as promised, and the redelivery is skipped as a duplicate of work that never happened. That is
// at-most-once wearing an at-least-once label. The first version of Wrap did exactly this.
func TestIdempotentRetriesAFailedMessage(t *testing.T) {
	idempotent := NewIdempotent()

	var attempts int

	handle := idempotent.Wrap(
		func(m kafka.Message) string { return string(m.Key) },
		func(context.Context, kafka.Message) error {
			attempts++
			if attempts == 1 {
				return errors.New("the database was briefly unavailable")
			}
			return nil
		},
	)

	msg := kafka.Message{Key: []byte("order-42")}

	if err := handle(context.Background(), msg); err == nil {
		t.Fatal("the first attempt should have failed")
	}

	// The redelivery.
	if err := handle(context.Background(), msg); err != nil {
		t.Fatalf("the redelivery failed: %v", err)
	}

	if attempts != 2 {
		t.Errorf("the handler ran %d time(s), want 2: the redelivery was skipped as a duplicate of a "+
			"failure", attempts)
	}

	// And once it has succeeded, a third delivery IS a duplicate.
	if err := handle(context.Background(), msg); err != nil {
		t.Fatal(err)
	}

	if attempts != 2 || idempotent.Duplicates() != 1 {
		t.Errorf("attempts=%d duplicates=%d after a success, want 2 and 1", attempts, idempotent.Duplicates())
	}
}
