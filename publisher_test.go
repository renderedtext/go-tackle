package tackle

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
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

func Test__PublisherConcurrentReconnectIsRaceFree(t *testing.T) {
	var dials int32
	var mu sync.Mutex
	var conns []*rabbit.Connection

	p, err := NewPublisher(options.URL, PublisherOptions{
		ConnectFunc: func() (*rabbit.Connection, error) {
			conn, err := rabbit.Dial(options.URL)
			if err != nil {
				return nil, err
			}

			atomic.AddInt32(&dials, 1)
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
			return conn, nil
		},
	})
	require.NoError(t, err)
	defer p.Close()

	require.NoError(t, p.ExchangeDeclare(options.RemoteExchange))

	// Establish the shared connection.
	require.NoError(t, p.Publish(&PublishParams{
		Body:       []byte(`"{}"`),
		Exchange:   options.RemoteExchange,
		RoutingKey: options.RoutingKey,
	}))
	require.Equal(t, int32(1), atomic.LoadInt32(&dials))

	// Drop the connection, then publish concurrently: all must recover on a
	// single shared reconnect. The previous implementation tripped -race here.
	mu.Lock()
	first := conns[0]
	mu.Unlock()
	require.NoError(t, first.Close())

	const publishers = 50
	start := make(chan struct{})
	wg := sync.WaitGroup{}
	errs := make(chan error, publishers)
	for i := 0; i < publishers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			errs <- p.PublishWithContext(ctx, &PublishParams{
				Body:       []byte(`"{}"`),
				Exchange:   options.RemoteExchange,
				RoutingKey: options.RoutingKey,
			})
		}()
	}

	close(start) // release all publishers at once to maximise reconnect contention
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}

	require.Equal(t, int32(2), atomic.LoadInt32(&dials), "one initial dial plus exactly one shared reconnect")
}

func Test__PublisherDialInFlightRespectsContext(t *testing.T) {
	release := make(chan struct{})
	var dials int32

	p, err := NewPublisher(options.URL, PublisherOptions{
		ConnectFunc: func() (*rabbit.Connection, error) {
			atomic.AddInt32(&dials, 1)
			<-release // block the in-flight dial until the test releases it
			return rabbit.Dial(options.URL)
		},
	})
	require.NoError(t, err)
	defer p.Close()

	// Leader goroutine: starts the (blocked) dial.
	leaderDone := make(chan struct{})
	go func() {
		defer close(leaderDone)
		_ = p.Publish(&PublishParams{Body: []byte(`"{}"`), Exchange: options.RemoteExchange, RoutingKey: options.RoutingKey})
	}()

	require.Eventually(t, func() bool { return atomic.LoadInt32(&dials) == 1 }, time.Second, 10*time.Millisecond)

	// A second caller with a short deadline must return at its deadline while
	// the leader's dial is still in flight — it must not start its own dial.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	err = p.PublishWithContext(ctx, &PublishParams{Body: []byte(`"{}"`), Exchange: options.RemoteExchange, RoutingKey: options.RoutingKey})
	elapsed := time.Since(start)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, elapsed, 2*time.Second)
	require.Equal(t, int32(1), atomic.LoadInt32(&dials), "the waiter must not start a competing dial")

	close(release)
	<-leaderDone
}

func Test__PublishDoesNotRetryForever(t *testing.T) {
	p, err := NewPublisher(options.URL, PublisherOptions{
		ConnectFunc: func() (*rabbit.Connection, error) {
			return nil, fmt.Errorf("failed to connect")
		},
	})
	require.NoError(t, err)
	defer p.Close()

	const publishers = 10
	errs := make(chan error, publishers)
	wg := sync.WaitGroup{}

	for i := 0; i < publishers; i++ {
		mi := i
		wg.Add(1)
		go func() {
			defer wg.Done()

			ctx, cancelFunc := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancelFunc()

			errs <- p.PublishWithContext(ctx, &PublishParams{
				Body:       []byte(fmt.Sprintf(`"%d"`, mi)),
				Exchange:   options.RemoteExchange,
				RoutingKey: options.RoutingKey,
			})
		}()
	}

	wg.Wait()
	close(errs)

	count := 0
	for e := range errs {
		count++
		require.ErrorIs(t, e, context.DeadlineExceeded)
	}
	require.Equal(t, publishers, count)
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

func Test__PublisherRecoversAfterFailureWithShortDeadlines(t *testing.T) {
	var dials int32
	var healthy atomic.Bool

	p, err := NewPublisher(options.URL, PublisherOptions{
		ConnectFunc: func() (*rabbit.Connection, error) {
			atomic.AddInt32(&dials, 1)
			if !healthy.Load() {
				return nil, fmt.Errorf("broker down")
			}
			return rabbit.Dial(options.URL)
		},
	})
	require.NoError(t, err)
	defer p.Close()

	// One real, failed dial arms the backoff.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	require.Error(t, p.PublishWithContext(ctx, &PublishParams{Body: []byte(`"{}"`), Exchange: options.RemoteExchange, RoutingKey: options.RoutingKey}))
	cancel()
	dialsAfterFailure := atomic.LoadInt32(&dials)
	require.GreaterOrEqual(t, dialsAfterFailure, int32(1))

	// Broker recovers. Callers with deadlines shorter than reconnectDelay must
	// still eventually redial and succeed. On the buggy version each such caller
	// re-armed nextDialAt without dialing, so connectFunc was never called again.
	healthy.Store(true)
	recovered := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		err := p.PublishWithContext(ctx, &PublishParams{Body: []byte(`"{}"`), Exchange: options.RemoteExchange, RoutingKey: options.RoutingKey})
		cancel()
		if err == nil {
			recovered = true
			break
		}
	}

	require.True(t, recovered, "publisher never recovered after the broker came back")
	require.Greater(t, atomic.LoadInt32(&dials), dialsAfterFailure, "connectFunc was never called again")
}

func Test__PublisherRecoversFromPanickingConnectFunc(t *testing.T) {
	var dials int32
	var panicNext atomic.Bool
	panicNext.Store(true)

	p, err := NewPublisher(options.URL, PublisherOptions{
		ConnectFunc: func() (*rabbit.Connection, error) {
			atomic.AddInt32(&dials, 1)
			if panicNext.Swap(false) {
				panic("connect func boom")
			}
			return rabbit.Dial(options.URL)
		},
	})
	require.NoError(t, err)
	defer p.Close()

	// A caller (e.g. behind recover middleware) recovers the panic.
	func() {
		defer func() { require.NotNil(t, recover(), "expected connectFunc to panic") }()
		_ = p.Publish(&PublishParams{Body: []byte(`"{}"`), Exchange: options.RemoteExchange, RoutingKey: options.RoutingKey})
	}()

	// The dialing latch must have been released; the next publish must proceed.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, p.PublishWithContext(ctx, &PublishParams{Body: []byte(`"{}"`), Exchange: options.RemoteExchange, RoutingKey: options.RoutingKey}))
	require.Equal(t, int32(2), atomic.LoadInt32(&dials))
}

func Test__PublisherCloseDuringDialDoesNotResurrect(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	var mu sync.Mutex
	var dialed *rabbit.Connection

	p, err := NewPublisher(options.URL, PublisherOptions{
		ConnectFunc: func() (*rabbit.Connection, error) {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-release
			conn, err := rabbit.Dial(options.URL)
			mu.Lock()
			dialed = conn
			mu.Unlock()
			return conn, err
		},
	})
	require.NoError(t, err)

	go func() {
		_ = p.Publish(&PublishParams{Body: []byte(`"{}"`), Exchange: options.RemoteExchange, RoutingKey: options.RoutingKey})
	}()

	<-entered // leader is inside connectFunc, blocked
	p.Close() // close while the dial is in flight
	close(release)

	// The connection dialled after Close must be closed, not stored.
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return dialed != nil && dialed.IsClosed()
	}, 3*time.Second, 50*time.Millisecond)

	// A subsequent publish returns a closed error, not a silent reconnect.
	require.ErrorIs(t, p.Publish(&PublishParams{Body: []byte(`"{}"`), Exchange: options.RemoteExchange, RoutingKey: options.RoutingKey}), ErrPublisherClosed)
}

func Test__ExchangeDeclarePreservesConnectErrorChain(t *testing.T) {
	sentinel := fmt.Errorf("dial refused by test")

	p, err := NewPublisher(options.URL, PublisherOptions{
		ConnectFunc: func() (*rabbit.Connection, error) { return nil, sentinel },
	})
	require.NoError(t, err)
	defer p.Close()

	err = p.ExchangeDeclare(options.RemoteExchange)
	require.ErrorIs(t, err, ErrConnectionUnavailable)
	require.ErrorIs(t, err, sentinel)
}
