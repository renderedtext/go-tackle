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

	assert.Eventually(t, func() bool { return counter.count == 1 }, time.Second, 100*time.Millisecond)
	consumer.Stop()
}

func TestConsumerRetry(t *testing.T) {
	receivedMessagesCount := 0
	timestamps := []time.Time{}

	//
	// Phase 1: Set up consumer that fails every time and collects
	//          information about the received messages
	//
	consumer := NewConsumer()

	deadHookExecuted := false
	options := Options{
		URL:            "amqp://guest:guest@rabbitmq:5672",
		RemoteExchange: "test.remote-exchange",
		Service:        "test.service",
		RoutingKey:     "test-routing-key",
		RetryDelay:     1,
		RetryLimit:     5,
		OnDeadFunc: func(d Delivery) {
			deadHookExecuted = true
		},
	}

	go consumer.Start(&options, func(d Delivery) error {
		receivedMessagesCount++
		timestamps = append(timestamps, time.Now())

		return fmt.Errorf("not able to handle it")
	})
	defer consumer.Stop()

	//
	// Phase 2: Purge the dead queue and make sure that we have a clean slate.
	//
	purgeDeadQueueInTests(t, consumer)

	//
	// Phase 3: Publish the message
	//
	params := PublishParams{
		Body:       []byte("hello"),
		Headers:    nil,
		AmqpURL:    options.URL,
		RoutingKey: options.RoutingKey,
		Exchange:   options.RemoteExchange,
	}
	err := PublishMessage(&params)
	assert.Nil(t, err)

	//
	// Phase 4: Assert that retry logic is working as advertised
	//
	t.Run("the message gets retried at least 5 times", func(t *testing.T) {
		assert.Eventually(t, func() bool {
			return receivedMessagesCount > 5
		}, 10*time.Second, 1*time.Second)
	})

	t.Run("the interval between retries is around 1 second", func(t *testing.T) {
		for i := 1; i < 5; i++ {
			duration := timestamps[i].Sub(timestamps[i-1])

			assert.InDelta(t, duration.Milliseconds(), 1000, 100)
		}
	})

	t.Run("the message ends up in the dead queue", func(t *testing.T) {
		deadQueue, err := consumer.channel.QueueInspect(options.GetDeadQueueName())
		assert.Nil(t, err)
		assert.Equal(t, 1, deadQueue.Messages)
		assert.True(t, deadHookExecuted)
	})

	t.Run("the message in the dead queue is the same one as oiginally sent", func(t *testing.T) {
		delivery, ok, err := consumer.channel.Get(options.GetDeadQueueName(), true)
		assert.Nil(t, err)
		assert.True(t, ok)
		assert.Equal(t, string(delivery.Body), "hello")
	})
}

func purgeDeadQueueInTests(t *testing.T, consumer *Consumer) {
	consumer.retryWithConstantWait("purge the dead queue", 5, 1*time.Second, func() error {
		if consumer.channel == nil {
			return fmt.Errorf("not yet ready for purging")
		}

		_, err := consumer.channel.QueuePurge(options.GetDeadQueueName(), false)
		return err
	})
}
