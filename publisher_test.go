package tackle

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	rabbit "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/require"
)

func Test__PublishMessage(t *testing.T) {
	counter := &struct {
		count int
	}{}

	// A publisher is being created here as well,
	// but I'm only interested in the consumer for this one.
	p, c := setup(t, counter, nil)
	defer p.Close()
	defer c.Stop()

	for i := 0; i < 10; i++ {
		go func() {
			require.NoError(t, PublishMessage(&PublishParams{
				AmqpURL:    options.URL,
				Body:       []byte(`"{}"`),
				Exchange:   options.RemoteExchange,
				RoutingKey: options.RoutingKey,
			}))
		}()
	}

	require.Eventually(t, func() bool { return counter.count == 10 }, 2*time.Second, 100*time.Millisecond)
	p.Close()
}

func Test__Publisher(t *testing.T) {
	t.Run("publish works", func(t *testing.T) {
		counter := &struct {
			count int
		}{}

		p, c := setup(t, counter, nil)
		defer p.Close()
		defer c.Stop()

		for i := 0; i < 10; i++ {
			go func() {
				require.NoError(t, p.Publish(&PublishParams{
					Body:       []byte(`"{}"`),
					Exchange:   options.RemoteExchange,
					RoutingKey: options.RoutingKey,
				}))
			}()
		}

		require.Eventually(t, func() bool { return counter.count == 10 }, 2*time.Second, 100*time.Millisecond)
	})

	t.Run("publish reconnects if connection is closed", func(t *testing.T) {
		counter := &struct {
			count int
		}{}

		p, c := setup(t, counter, nil)
		defer p.Close()
		defer c.Stop()

		// Connection is created lazily, so this will create it
		require.NoError(t, p.Publish(&PublishParams{
			Body:       []byte(`"{}"`),
			Exchange:   options.RemoteExchange,
			RoutingKey: options.RoutingKey,
		}))

		// Close connection and publish more messages
		require.NoError(t, p.connection.Close())
		for i := 0; i < 10; i++ {
			go func() {
				require.NoError(t, p.Publish(&PublishParams{
					Body:       []byte(`"{}"`),
					Exchange:   options.RemoteExchange,
					RoutingKey: options.RoutingKey,
				}))
			}()
		}

		// Connection is re-created and messages are published
		require.Eventually(t, func() bool { return counter.count == 11 }, 5*time.Second, 500*time.Millisecond)
	})
}

func Test__PublishDoesNotRetryForever(t *testing.T) {
	counter := &struct {
		count int
	}{}

	p, c := setup(t, counter, func() (*rabbit.Connection, error) {
		return nil, fmt.Errorf("failed to connect")
	})

	defer p.Close()
	defer c.Stop()

	errs := []error{}
	wg := sync.WaitGroup{}

	for i := 0; i < 10; i++ {
		mi := i
		wg.Add(1)
		go func() {
			defer wg.Done()

			ctx, cancelFunc := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancelFunc()

			err := p.PublishWithContext(ctx, &PublishParams{
				Body:       []byte(fmt.Sprintf(`"%d"`, mi)),
				Exchange:   options.RemoteExchange,
				RoutingKey: options.RoutingKey,
			})

			if err != nil {
				errs = append(errs, err)
			}
		}()
	}

	wg.Wait()

	for _, e := range errs {
		require.ErrorContains(t, e, "context deadline exceeded")
	}
}

func setup(t *testing.T, counter *struct{ count int }, connectFunc func() (*rabbit.Connection, error)) (*Publisher, *Consumer) {
	p, err := NewPublisher(options.URL, PublisherOptions{
		ConnectFunc: connectFunc,
	})

	require.NoError(t, err)

	consumer := NewConsumer()
	go func() {
		err := consumer.Start(&options, func(delivery Delivery) error {
			counter.count++
			return nil
		})
		require.Nil(t, err)
	}()

	require.Eventually(t, func() bool { return consumer.State == StateListening }, time.Second, 100*time.Millisecond)
	return p, consumer
}
