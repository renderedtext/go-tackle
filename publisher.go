package tackle

import (
	"context"
	"errors"
	"sync"
	"time"

	rabbit "github.com/rabbitmq/amqp091-go"
)

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
	publisher, err := NewPublisher(params.AmqpURL, nil)
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
	connection         *rabbit.Connection
	connectFunc        func() (*rabbit.Connection, error)
	connectOnce        sync.Once
	connectionErr      error
	reconnectionLock   sync.Mutex
	connectionInFlight bool

	logger  Logger
	amqpURL string
}

func NewPublisher(amqpURL string, connectFunc func() (*rabbit.Connection, error)) (*Publisher, error) {
	p := Publisher{
		logger:      &defaultLogger{},
		amqpURL:     amqpURL,
		connectFunc: connectFunc,
	}

	if p.connectFunc == nil {
		p.connectFunc = p.connect
	}

	return &p, nil
}

func (p *Publisher) SetLogger(l Logger) {
	p.logger = l
}

func (p *Publisher) connect() (*rabbit.Connection, error) {
	p.logger.Infof("Connecting...")
	return rabbit.Dial(p.amqpURL)
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
