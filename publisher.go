package tackle

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	rabbit "github.com/rabbitmq/amqp091-go"
)

const defaultConnectionTimeout = 5 * time.Second

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

	if err != nil {
		return err
	}

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
	connectionName     string
	connection         *rabbit.Connection
	connectionTimeout  time.Duration
	connectFunc        func() (*rabbit.Connection, error)
	connectOnce        sync.Once
	connectionErr      error
	reconnectionLock   sync.Mutex
	connectionInFlight bool

	logger  Logger
	amqpURL string
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
	p.connectOnce.Do(func() {
		p.connection, p.connectionErr = p.connectFunc()
		p.connectionInFlight = false
	})

	return p.connection, p.connectionErr
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
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			err := p.publishWithContext(ctx, params)
			if err == nil {
				return nil
			}

			// If this is not a connection error, we return the error.
			if p.connectionErr == nil {
				return err
			}

			p.logger.Errorf("Error getting connection for %s: %v - retrying", string(params.Body), err)
			p.reconnect()
		}
	}
}

func (p *Publisher) publishWithContext(ctx context.Context, params *PublishParams) error {
	connection, err := p.getConnection()
	if err != nil {
		return err
	}

	channel, err := connection.Channel()
	if err == nil {
		defer channel.Close()

		msg := rabbit.Publishing{
			Body:         params.Body,
			Headers:      params.Headers,
			DeliveryMode: rabbit.Persistent,
		}

		return channel.PublishWithContext(ctx, params.Exchange, params.RoutingKey, params.IsMandatory, params.IsImmediate, msg)
	}

	// If we're not dealing with the connection being closed, just return.
	if !errors.Is(err, rabbit.ErrClosed) {
		return err
	}

	// If the connection is closed, we try to re-connect.
	// After that, we re-publish the message.
	return p.reconnectAndPublish(ctx, params)
}

func (p *Publisher) reconnectAndPublish(ctx context.Context, params *PublishParams) error {
	p.reconnectionLock.Lock()
	defer p.reconnectionLock.Unlock()

	// We only update the sync.Once controlling the connection if the connection is closed.
	// The connection being closed should only happen for the first message coming through.
	if !p.connectionInFlight {
		p.connectionInFlight = true
		p.connectOnce = sync.Once{}
	}

	return p.publishWithContext(ctx, params)
}

func (p *Publisher) reconnect() {
	p.reconnectionLock.Lock()
	defer p.reconnectionLock.Unlock()

	if !p.connectionInFlight {
		p.connectionInFlight = true

		// We wait a little bit before allowing a reconnect to happen
		// to ensure we are not bombarding the RabbitMQ with reconnection attempts.
		time.Sleep(time.Second)

		p.connectOnce = sync.Once{}
	}
}

func (p *Publisher) Close() {
	if p.connection != nil && !p.connection.IsClosed() {
		err := p.connection.Close()
		if err != nil {
			p.logger.Errorf("failed to close publisher connection %v", err)
		}

		p.connection = nil
	}
}
