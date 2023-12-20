package tackle

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func Test__Publisher(t *testing.T) {
	p, err := NewPublisher(options.URL)
	require.NoError(t, err)

	counter := &struct {
		count int
	}{}

	consumer := NewConsumer()
	go func() {
		err := consumer.Start(&options, func(delivery Delivery) error {
			counter.count++
			return nil
		})
		require.Nil(t, err)
	}()

	require.Eventually(t, func() bool { return consumer.State == StateListening }, time.Second, 100*time.Millisecond)

	t.Run("publish works", func(t *testing.T) {
		for i := 0; i < 10; i++ {
			mi := i
			go func() {
				require.NoError(t, p.Publish(&PublishParams{
					Body:       []byte(fmt.Sprintf(`"{%d}"`, mi)),
					Exchange:   options.RemoteExchange,
					RoutingKey: options.RoutingKey,
				}))
			}()
		}

		require.Eventually(t, func() bool { return counter.count == 10 }, 2*time.Second, 100*time.Millisecond)
	})

	t.Run("publish reconnects if connection is closed", func(t *testing.T) {
		counter.count = 0
		require.NoError(t, p.connection.Close())

		for i := 0; i < 10; i++ {
			mi := i
			go func() {
				require.NoError(t, p.Publish(&PublishParams{
					Body:       []byte(fmt.Sprintf(`"{"id": %d}"`, mi)),
					Exchange:   options.RemoteExchange,
					RoutingKey: options.RoutingKey,
				}))
			}()
		}

		require.Eventually(t, func() bool { return counter.count == 10 }, 5*time.Second, 500*time.Millisecond)
	})
}
