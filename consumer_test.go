package tackle

import (
	"fmt"
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
		err := amqpConsumer.Start(&options, func(Delivery) error { return nil })
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
		err := consumer.Start(&options, func(Delivery) error { return nil })
		if err != nil {
			t.Error(err)
		}
	}()
	defer consumer.Stop()
	assert.Eventually(t, func() bool { return consumer.State == StateListening }, time.Second, 100*time.Millisecond)

	err := consumer.Start(&options, func(delivery Delivery) error { return nil })
	assert.NotNil(t, err)
}

func TestProcessorRanOnceAndPublishWork(t *testing.T) {
	counter := &struct {
		count int
	}{}

	consumer := NewConsumer()
	go func() {
		err := consumer.Start(&options, func(delivery Delivery) error {
			counter.count++
			return nil
		})
		assert.Nil(t, err)
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
	assert.Nil(t, err)

	assert.Eventually(t, func() bool { return 1 == counter.count }, time.Second, 100*time.Millisecond)
	consumer.Stop()
}

func TestConsumerRetry(t *testing.T) {
	consumer := NewConsumer()
	counter := 0

	options := Options{
		URL:            "amqp://guest:guest@rabbitmq:5672",
		RemoteExchange: "test.remote-exchange",
		Service:        "test.service",
		RoutingKey:     "test-routing-key",
		RetryDelay:     1,
		RetryLimit:     5,
	}

	go consumer.Start(&options, func(d Delivery) error {
		counter++
		fmt.Printf("processing, %s\n", string(d.Body()))

		return fmt.Errorf("not able to handle it")
	})
	defer consumer.Stop()

	params := PublishParams{
		Body:       []byte("hello"),
		Headers:    nil,
		AmqpURL:    options.URL,
		RoutingKey: options.RoutingKey,
		Exchange:   options.RemoteExchange,
	}
	err := PublishMessage(&params)
	assert.Nil(t, err)

	assert.Eventually(t, func() bool { return counter > 5 }, 10*time.Second, 1*time.Second)
	assert.Equal(t, 1, consumer.MessagesSentToDeadQueue)
}
