package tackle

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	rabbit "github.com/rabbitmq/amqp091-go"
)

const (
	defaultConnectionTimeout = 5 * time.Second
	reconnectDelay           = time.Second
)

// errConnectionUnavailable marks failures worth retrying on a fresh connection
// (dial failure, or a connection closed before a message could be sent), as
// opposed to a publish the broker actively rejected, which is returned as-is.
var errConnectionUnavailable = errors.New("connection unavailable")

type PublishParams struct {
	Body    []byte
	Headers rabbit.Table

	AmqpURL    string
	RoutingKey string
	Exchange   string

	IsMandatory bool
	IsImmediate bool
}

func PublishMessage(params *PublishParams) error {
	publisher, err := NewPublisher(params.AmqpURL, PublisherOptions{})
	if err != nil {
		return err
	}

	defer publisher.Close()

	err = publisher.ExchangeDeclare(params.Exchange)
	if err != nil {
		return err
	}

	return publisher.Publish(params)
}

type Publish interface {
	Publish(*PublishParams) error
}

type Publisher struct {
	connectionName    string
	connectionTimeout time.Duration
	connectFunc       func() (*rabbit.Connection, error)

	logger  Logger
	amqpURL string

	// mu guards connection, dialing and nextDialAt. Every read and write of
	// connection goes through it, so a reconnect can never race a concurrent
	// publish. Dialing itself happens without mu held (see getConnection).
	mu         sync.Mutex
	connection *rabbit.Connection
	dialing    chan struct{}
	nextDialAt time.Time
}

type PublisherOptions struct {
	ConnectionName    string
	ConnectionTimeout time.Duration
	ConnectFunc       func() (*rabbit.Connection, error)
}

func NewPublisher(amqpURL string, options PublisherOptions) (*Publisher, error) {
	p := Publisher{
		logger:            &defaultLogger{},
		amqpURL:           amqpURL,
		connectionName:    options.ConnectionName,
		connectFunc:       options.ConnectFunc,
		connectionTimeout: options.ConnectionTimeout,
	}

	if p.connectFunc == nil {
		p.connectFunc = p.connect
	}

	if p.connectionName == "" {
		p.connectionName = "go-tackle-publisher"
	}

	if p.connectionTimeout == 0 {
		p.connectionTimeout = defaultConnectionTimeout
	}

	return &p, nil
}

func (p *Publisher) SetConnectionName(connName string) {
	p.connectionName = connName
}

func (p *Publisher) SetLogger(l Logger) {
	p.logger = l
}

func (p *Publisher) connect() (*rabbit.Connection, error) {
	p.logger.Infof("Connecting...")

	config := rabbit.Config{
		Properties: rabbit.Table{"connection_name": p.connectionName},
		Dial: func(network, addr string) (net.Conn, error) {
			conn, err := net.DialTimeout(network, addr, p.connectionTimeout)
			if err != nil {
				return nil, err
			}

			// Heartbeating hasn't started yet, don't stall forever on a dead server.
			// A deadline is set for TLS and AMQP handshaking. After AMQP is established,
			// the deadline is cleared in openComplete.
			if err := conn.SetDeadline(time.Now().Add(p.connectionTimeout)); err != nil {
				return nil, err
			}

			return conn, nil
		},
	}

	return rabbit.DialConfig(p.amqpURL, config)
}

// getConnection returns a live connection. If none exists it dials one, but
// only a single dial is ever in flight: the first caller becomes the dialer
// and the rest wait for its result or their own context, whichever comes
// first. Dialing happens without mu held, so a slow dial never blocks other
// callers past their deadline, and failed attempts are paced by nextDialAt so
// an outage cannot turn into a reconnect storm.
func (p *Publisher) getConnection(ctx context.Context) (*rabbit.Connection, error) {
	for {
		p.mu.Lock()

		if p.connection != nil && !p.connection.IsClosed() {
			connection := p.connection
			p.mu.Unlock()
			return connection, nil
		}

		if p.dialing != nil {
			dialing := p.dialing
			p.mu.Unlock()

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-dialing:
				continue
			}
		}

		dialing := make(chan struct{})
		p.dialing = dialing
		backoff := time.Until(p.nextDialAt)
		p.mu.Unlock()

		connection, err := p.dial(ctx, backoff)

		p.mu.Lock()
		if err == nil {
			p.connection = connection
		} else {
			p.nextDialAt = time.Now().Add(reconnectDelay)
		}
		p.dialing = nil
		close(dialing)
		p.mu.Unlock()

		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
				return nil, err
			}
			return nil, fmt.Errorf("%w: %v", errConnectionUnavailable, err)
		}
		return connection, nil
	}
}

func (p *Publisher) dial(ctx context.Context, backoff time.Duration) (*rabbit.Connection, error) {
	if backoff > 0 && !wait(ctx, backoff) {
		return nil, ctx.Err()
	}

	return p.connectFunc()
}

func (p *Publisher) ExchangeDeclare(exchange string) error {
	connection, err := p.getConnection(context.Background())
	if err != nil {
		return err
	}

	channel, err := connection.Channel()
	if err != nil {
		return err
	}

	defer channel.Close()
	return channel.ExchangeDeclare(exchange, "direct", Durable, AutoDeleted, Internal, NoWait, nil)
}

func (p *Publisher) Publish(params *PublishParams) error {
	return p.PublishWithContext(context.Background(), params)
}

func (p *Publisher) PublishWithContext(ctx context.Context, params *PublishParams) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		err := p.publish(ctx, params)
		if err == nil {
			return nil
		}

		// Only connection-level failures are retryable; a publish the broker
		// rejected (or a cancelled context) is returned to the caller. Pacing
		// between attempts is handled by getConnection's nextDialAt backoff.
		if !errors.Is(err, errConnectionUnavailable) {
			return err
		}

		p.logger.Errorf("Error publishing %s: %v - retrying", string(params.Body), err)
	}
}

func (p *Publisher) publish(ctx context.Context, params *PublishParams) error {
	connection, err := p.getConnection(ctx)
	if err != nil {
		return err
	}

	channel, err := connection.Channel()
	if err != nil {
		// A connection that closed before we could open a channel is transient:
		// no message was sent, so retrying on a fresh connection is safe.
		if errors.Is(err, rabbit.ErrClosed) || connection.IsClosed() {
			return fmt.Errorf("%w: %v", errConnectionUnavailable, err)
		}
		return err
	}

	defer channel.Close()

	return channel.PublishWithContext(ctx, params.Exchange, params.RoutingKey, params.IsMandatory, params.IsImmediate, rabbit.Publishing{
		Body:         params.Body,
		Headers:      params.Headers,
		DeliveryMode: rabbit.Persistent,
	})
}

func wait(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (p *Publisher) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.connection != nil && !p.connection.IsClosed() {
		if err := p.connection.Close(); err != nil {
			p.logger.Errorf("failed to close publisher connection %v", err)
		}
	}

	p.connection = nil
}
