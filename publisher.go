package tackle

import (
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
	publisher, err := NewPublisher(params.AmqpURL)
	if err != nil {
		return err
	}

	err = publisher.Connect()
	defer publisher.Close()

	if err != nil {
		return err
	}

	return publisher.Publish(params)
}

type Publish interface {
	Publish(*PublishParams) error
}

type Publisher struct {
	connection *rabbit.Connection
	channel    *rabbit.Channel
	logger     Logger
	amqpUrl    string
}

func NewPublisher(amqpUrl string) (*Publisher, error) {
	return &Publisher{
		logger:  &defaultLogger{},
		amqpUrl: amqpUrl,
	}, nil
}

func (p *Publisher) SetLogger(l Logger) {
	p.logger = l
}

func (p *Publisher) Connect() error {
	connection, err := rabbit.Dial(p.amqpUrl)
	if err != nil {
		return err
	}

	p.connection = connection

	channel, err := connection.Channel()
	if err != nil {
		return err
	}

	p.channel = channel

	return nil
}

func (p *Publisher) Publish(params *PublishParams) error {
	msg := rabbit.Publishing{
		Body:         params.Body,
		Headers:      params.Headers,
		DeliveryMode: rabbit.Persistent,
	}

	return p.channel.Publish(params.Exchange, params.RoutingKey, params.IsMandatory, params.IsImmediate, msg)
}

func (p *Publisher) Close() {
	if p.channel != nil && !p.channel.IsClosed() {
		err := p.channel.Close()
		if err != nil {
			p.logger.Errorf("failed to close publisher channel %v", err)
		}
	}

	if p.connection != nil && !p.connection.IsClosed() {
		err := p.connection.Close()
		if err != nil {
			p.logger.Errorf("failed to close publisher connection %v", err)
		}
	}
}
