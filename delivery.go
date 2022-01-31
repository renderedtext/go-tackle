package tackle

import (
	rabbit "github.com/rabbitmq/amqp091-go"
)

type Delivery interface {
	Ack() error
	Nack(requeue bool) error
	Body() []byte
}

type amqpDelivery struct {
	dlry *rabbit.Delivery
}

func NewDelivery(d *rabbit.Delivery) Delivery {
	return &amqpDelivery{dlry: d}
}

func (d *amqpDelivery) Ack() error {
	return d.dlry.Ack(false)
}

func (d *amqpDelivery) Nack(requeue bool) error {
	return d.dlry.Nack(false, requeue)
}

func (d *amqpDelivery) Body() []byte {
	return d.dlry.Body
}

type fakeDelivery struct {
	body []byte
}

func NewFakeDelivery(body []byte) Delivery {
	return &fakeDelivery{body: body}
}

func (f *fakeDelivery) Ack() error {
	return nil
}

func (f *fakeDelivery) Nack(_ bool) error {
	return nil
}

func (f *fakeDelivery) Retry(_ string) {
}

func (f *fakeDelivery) Body() []byte {
	return f.body
}
