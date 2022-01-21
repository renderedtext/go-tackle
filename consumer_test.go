package tackle

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var options = Options{
	URL:            "amqp://guest:guest@rabbitmq:5672",
	RemoteExchange: "test.remote-exchange",
	Service:        "test.service",
	RoutingKey:     "test-routing-key",
}

func TestConsumerCanBeStartedAndStopped(t *testing.T) {
	amqpConsumer := NewConsumer()
	go func() {
		err := amqpConsumer.Start(&options, func(Delivery) {})
		if err != nil {
			t.Error(err)
		}

	}()
	assert.Eventually(t, func() bool { return amqpConsumer.State == StateListening },
		time.Second, 100*time.Millisecond)
	amqpConsumer.Stop()
	assert.Eventually(t, func() bool { return amqpConsumer.State == StateNotListening },
		time.Second, 100*time.Millisecond)
}

func TestConsumerCannotBeStartedTwice(t *testing.T) {
	consumer := NewConsumer()
	go func() {
		err := consumer.Start(&options, func(Delivery) {})
		if err != nil {
			t.Error(err)
		}
	}()
	defer consumer.Stop()
	assert.Eventually(t, func() bool { return consumer.State == StateListening }, time.Second, 100*time.Millisecond)

	err := consumer.Start(&options, func(delivery Delivery) {})
	assert.NotNil(t, err)
}

func TestProcessorRanOnceAndPublishWork(t *testing.T) {
	counter := &struct {
		count int
	}{}

	consumer := NewConsumer()
	go func() {
		err := consumer.Start(&options, func(delivery Delivery) {
			counter.count++
			delivery.Ack()
		})
		if err != nil {
			t.Error(err)
		}
	}()

	assert.Eventually(t, func() bool { return consumer.State == StateListening }, time.Second, 100*time.Millisecond)
	params := PublishParams{
		Body:       []byte("{'test': 'message' }"),
		Headers:    nil,
		AmqpURL:    options.URL,
		RoutingKey: options.RoutingKey,
		Exchange:   options.RemoteExchange,
	}
	err := PublishMessage(&params)
	if err != nil {
		t.Error(err)
	}
	assert.Eventually(t, func() bool { return 1 == counter.count }, time.Second, 100*time.Millisecond)
	consumer.Stop()
}
