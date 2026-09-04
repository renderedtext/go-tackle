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

// ErrConnectionUnavailable wraps failures worth retrying on a fresh connection
// (a dial failure, or a connection closed before a message could be sent), as
// opposed to a publish the broker actively rejected, which is returned as-is.
// It is exported so callers can classify retryable failures with errors.Is.
var ErrConnectionUnavailable = errors.New("connection unavailable")

// ErrPublisherClosed is returned once Close has been called.
var ErrPublisherClosed = errors.New("publisher is closed")

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

	// mu guards connection, dialing, nextDialAt and closed. Every read and
	// write of connection goes through it, so a reconnect can never race a
	// concurrent publish. Dialing itself happens without mu held (see dial).
	mu         sync.Mutex
	connection *rabbit.Connection
	dialing    chan struct{}
	nextDialAt time.Time
	closed     bool
	// closeCh is closed exactly once by Close so backoff waits and followers
	// waiting on a dial are released promptly instead of blocking on a timer or
	// an in-flight connect.
	closeCh chan struct{}
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
		closeCh:           make(chan struct{}),
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

	var socket net.Conn
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
				_ = conn.Close()
				return nil, err
			}

			socket = conn
			return conn, nil
		},
	}

	connection, err := rabbit.DialConfig(p.amqpURL, config)
	// On a TLS-config/handshake failure DialConfig returns (nil, err) without
	// closing the socket our Dial produced; close it so it can't leak. When it
	// returns a non-nil connection (including on an AMQP-handshake error) the
	// caller's discard path closes it, so we leave it alone here.
	if err != nil && connection == nil && socket != nil {
		_ = socket.Close()
	}
	return connection, err
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

		if p.closed {
			p.mu.Unlock()
			return nil, ErrPublisherClosed
		}

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
			case <-p.closeCh:
				return nil, ErrPublisherClosed
			case <-dialing:
				continue
			}
		}

		dialing := make(chan struct{})
		p.dialing = dialing
		backoff := time.Until(p.nextDialAt)
		p.mu.Unlock()

		connection, err := p.dial(ctx, backoff, dialing)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
				return nil, err
			}
			if errors.Is(err, ErrPublisherClosed) {
				return nil, err
			}
			return nil, fmt.Errorf("%w: %w", ErrConnectionUnavailable, err)
		}
		return connection, nil
	}
}

// dial performs a single dial attempt on behalf of all current waiters. It
// always releases the dialing latch and records the outcome under mu — even if
// connectFunc panics — so a recovered panic cannot wedge the publisher. The
// backoff is only applied to, and nextDialAt only re-armed by, an attempt that
// actually called connectFunc: a caller that abandons during backoff must not
// push the next attempt further out, or short-deadline callers would livelock.
func (p *Publisher) dial(ctx context.Context, backoff time.Duration, dialing chan struct{}) (connection *rabbit.Connection, err error) {
	attempted := false
	installed := false

	defer func() {
		p.mu.Lock()
		// Pace the next attempt after every real dial — success included — so a
		// connection that is dead on arrival cannot drive a tight reconnect loop.
		if attempted {
			p.nextDialAt = time.Now().Add(reconnectDelay)
		}
		switch {
		case p.closed:
			err = ErrPublisherClosed
		case err != nil:
			// keep the dial error
		case connection == nil:
			err = errors.New("connect func returned a nil connection")
		case connection.IsClosed():
			err = errors.New("connect func returned a closed connection")
		default:
			p.connection = connection
			installed = true
		}
		// Any connection we dialled but will not use (Close raced, a (conn, err)
		// return, or a dead-on-arrival connection) must be closed, bounded, so it
		// cannot leak a socket and reader goroutine.
		if connection != nil && !installed {
			go p.discard(connection)
		}
		p.dialing = nil
		close(dialing)
		p.mu.Unlock()
	}()

	if backoff > 0 {
		timer := time.NewTimer(backoff)
		defer timer.Stop()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-p.closeCh:
			return nil, ErrPublisherClosed
		case <-timer.C:
		}
	}

	// Don't start a dial that Close has already superseded — important for a
	// slow or blocking connectFunc.
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return nil, ErrPublisherClosed
	}

	attempted = true
	return p.connectFunc()
}

func (p *Publisher) discard(connection *rabbit.Connection) {
	if err := connection.CloseDeadline(time.Now().Add(p.connectionTimeout)); err != nil && !errors.Is(err, rabbit.ErrClosed) {
		p.logger.Errorf("failed to discard publisher connection %v", err)
	}
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
		// rejected (or a cancelled/closed publisher) is returned to the caller.
		// Pacing between attempts is handled by getConnection's nextDialAt backoff.
		if !errors.Is(err, ErrConnectionUnavailable) {
			return err
		}

		p.logger.Errorf("Error publishing to %s/%s (%d bytes): %v - retrying", params.Exchange, params.RoutingKey, len(params.Body), err)
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
			return fmt.Errorf("%w: %w", ErrConnectionUnavailable, err)
		}
		return err
	}

	defer channel.Close()

	// The result of a publish is ambiguous: amqp091 does not guarantee whether
	// the broker received the message, so its errors (including ErrClosed) are
	// returned to the caller rather than retried, which could duplicate.
	return channel.PublishWithContext(ctx, params.Exchange, params.RoutingKey, params.IsMandatory, params.IsImmediate, rabbit.Publishing{
		Body:         params.Body,
		Headers:      params.Headers,
		DeliveryMode: rabbit.Persistent,
	})
}

func (p *Publisher) Close() {
	p.mu.Lock()
	if !p.closed {
		p.closed = true
		close(p.closeCh)
	}
	connection := p.connection
	p.connection = nil
	p.mu.Unlock()

	if connection != nil && !connection.IsClosed() {
		// Bound the close: Connection.Close is a synchronous RPC that, against a
		// frozen broker, blocks until dead-peer detection. Done outside mu so it
		// never stalls concurrent publishers.
		if err := connection.CloseDeadline(time.Now().Add(p.connectionTimeout)); err != nil {
			p.logger.Errorf("failed to close publisher connection %v", err)
		}
	}
}
