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

// errConnectionUnavailable marks failures that are worth retrying on a fresh
// connection (dial failure or a closed connection), as opposed to a publish
// that the broker actively rejected, which is returned to the caller as-is.
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

	// mu guards connection. Every read and write of connection goes through
	// it, so a reconnect can never race a concurrent publish. Only one
	// goroutine dials at a time; the others reuse the connection it stores.
	mu         sync.Mutex
	connection *rabbit.Connection
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

func (p *Publisher) getConnection() (*rabbit.Connection, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.connection != nil && !p.connection.IsClosed() {
		return p.connection, nil
	}

	connection, err := p.connectFunc()
	if err != nil {
		return nil, err
	}

	p.connection = connection
	return connection, nil
}

func (p *Publisher) ExchangeDeclare(exchange string) error {
	connection, err := p.getConnection()
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
		// rejected (or a cancelled context) is returned to the caller.
		if !errors.Is(err, errConnectionUnavailable) {
			return err
		}

		p.logger.Errorf("Error publishing %s: %v - retrying", string(params.Body), err)
		if !wait(ctx, reconnectDelay) {
			return ctx.Err()
		}
	}
}

func (p *Publisher) publish(ctx context.Context, params *PublishParams) error {
	connection, err := p.getConnection()
	if err != nil {
		return fmt.Errorf("%w: %v", errConnectionUnavailable, err)
	}

	channel, err := connection.Channel()
	if err != nil {
		if errors.Is(err, rabbit.ErrClosed) {
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
