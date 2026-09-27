// Package kafkatest is the broker half of this module's test harness.
//
// Same decisions as internal/pgtest and internal/redistest: skip when there is no broker rather than fail, and
// keep each test binary's data out of the others' way.
//
// Isolation here is per TOPIC rather than per database, because Kafka has no equivalent of a database and
// creating a cluster per test binary is not a thing. Each test gets a topic named after itself plus a random
// suffix, which also means a test can be run twice without the second run seeing the first one's messages.
package kafkatest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
)

// DefaultBroker is the Redpanda from this module's docker-compose.yml.
//
// 19092 rather than 9092, so it does not collide with a Kafka someone already runs, and because Redpanda's
// external listener is conventionally on a different port from its internal one.
const DefaultBroker = "localhost:19092"

// Brokers returns the broker list from the environment or the default.
func Brokers() []string {
	if b := os.Getenv("KAFKA_BROKERS"); b != "" {
		return strings.Split(b, ",")
	}
	return []string{DefaultBroker}
}

var (
	once      sync.Once
	available bool
	checkErr  error
)

// Require skips the test unless a broker is reachable.
func Require(t testing.TB) []string {
	t.Helper()

	once.Do(func() { available, checkErr = check() })

	if !available {
		t.Skipf("no Kafka broker at %v (%v)\n"+
			"  docker compose up -d, or point it elsewhere: KAFKA_BROKERS=host:9092 go test ./...",
			Brokers(), checkErr)
	}

	return Brokers()
}

func check() (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := kafka.DialContext(ctx, "tcp", Brokers()[0])
	if err != nil {
		return false, err
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.Brokers(); err != nil {
		return false, err
	}

	return true, nil
}

// Topic creates a topic for this test and deletes it afterwards.
//
// # Why the partition count is a parameter
//
// Because it is the scaling decision and half the tests here are about what it does. A helper that always
// creates one partition would make every ordering test pass for the wrong reason: with one partition everything
// is ordered, including the things that should not be.
func Topic(t testing.TB, partitions int) string {
	t.Helper()

	brokers := Require(t)

	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("generating a topic suffix: %v", err)
	}

	name := fmt.Sprintf("test-%s-%s", sanitise(t.Name()), hex.EncodeToString(suffix))

	conn, err := kafka.Dial("tcp", brokers[0])
	if err != nil {
		t.Fatalf("dialling: %v", err)
	}
	defer func() { _ = conn.Close() }()

	controller, err := conn.Controller()
	if err != nil {
		t.Fatalf("finding the controller: %v", err)
	}

	// Topic creation goes to the CONTROLLER broker, not to whichever broker answered first. On a
	// single-node cluster they are the same and on a real one they are not, which is why the
	// two-step dial is the documented way and a direct CreateTopics call works in development and
	// fails in staging.
	controllerConn, err := kafka.Dial("tcp",
		fmt.Sprintf("%s:%d", controller.Host, controller.Port))
	if err != nil {
		t.Fatalf("dialling the controller: %v", err)
	}
	defer func() { _ = controllerConn.Close() }()

	if err := controllerConn.CreateTopics(kafka.TopicConfig{
		Topic:             name,
		NumPartitions:     partitions,
		ReplicationFactor: 1,
	}); err != nil {
		t.Fatalf("creating topic %s: %v", name, err)
	}

	// A FRESH connection in the cleanup. The deferred Close above runs when Topic returns, long
	// before the cleanup does, so reusing controllerConn here fails with
	// "use of closed network connection" on every single test.
	t.Cleanup(func() {
		conn, err := kafka.Dial("tcp", fmt.Sprintf("%s:%d", controller.Host, controller.Port))
		if err != nil {
			t.Logf("dialling to delete topic %s: %v", name, err)
			return
		}
		defer func() { _ = conn.Close() }()

		if err := conn.DeleteTopics(name); err != nil {
			t.Logf("deleting topic %s: %v", name, err)
		}
	})

	// Creation is asynchronous: CreateTopics returns before every broker knows about the topic, and
	// a producer that writes immediately can get UNKNOWN_TOPIC_OR_PARTITION. Waiting for the
	// metadata to show the expected partition count is the reliable form, and a sleep is the
	// version that is flaky on a loaded machine.
	waitForPartitions(t, brokers[0], name, partitions)

	return name
}

func waitForPartitions(t testing.TB, broker, topic string, want int) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)

	for time.Now().Before(deadline) {
		conn, err := kafka.Dial("tcp", broker)
		if err != nil {
			t.Fatalf("dialling: %v", err)
		}

		partitions, err := conn.ReadPartitions(topic)
		_ = conn.Close()

		if err == nil && len(partitions) == want {
			return
		}

		time.Sleep(100 * time.Millisecond)
	}

	t.Fatalf("topic %s did not reach %d partitions within 10s", topic, want)
}

// Group returns a unique consumer group id for a test.
//
// Unique per RUN, not just per test. A group id remembers its offsets in Kafka, so reusing one means the second
// run of a test starts where the first left off and reads nothing, which looks like a broken producer.
func Group(t testing.TB) string {
	t.Helper()

	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("generating a group suffix: %v", err)
	}

	return fmt.Sprintf("group-%s-%s", sanitise(t.Name()), hex.EncodeToString(suffix))
}

// sanitise makes a test name usable in a topic name.
//
// Kafka allows [a-zA-Z0-9._-] and a test name can contain slashes from subtests and spaces from names with
// them. A topic with an invalid character fails at creation with a message that does not say which character.
func sanitise(name string) string {
	var b strings.Builder

	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}

	out := strings.Trim(b.String(), "-")

	// Kafka's limit is 249 characters and a subtest name can be long.
	if len(out) > 100 {
		out = out[:100]
	}

	return out
}
