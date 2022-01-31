package tackle

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	rabbit "github.com/rabbitmq/amqp091-go"
)

const (
	StateListening    = "listening"
	StateNotListening = "not-listening"

	ReconnectionAttempts      = 50
	ReconnectionDelayDuration = 2 * time.Second

	PrefetchCount = 1
	PrefetchSize  = 0
	Global        = true

	ConsumerName = ""
	Durable      = true
	Exclusive    = false
	AutoAck      = false
	AutoDeleted  = false
	Internal     = false
	NoWait       = false
	NoLocal      = false
)

type ProcessorFunc func(Delivery) error

type Consumer struct {
	options    *Options
	connection *rabbit.Connection
	channel    *rabbit.Channel
	shutdown   chan bool
	State      string
	processor  ProcessorFunc
	mu         sync.Mutex
	logger     Logger
}

func NewConsumer() *Consumer {
	return &Consumer{
		State:  StateNotListening,
		logger: &defaultLogger{},
	}
}

func (c *Consumer) SetLogger(l Logger) {
	c.logger = l
}

func (c *Consumer) Start(options *Options, f ProcessorFunc) error {
	if c.State == StateListening {
		return errors.New("consumer is already listening")
	}

	c.processor = f
	err := c.connect(options)
	if err != nil {
		return err
	}
	defer c.close()

	return c.consume()
}

func (c *Consumer) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.State == StateNotListening {
		return
	}

	c.logger.Infof("Stopped listening for messages in %s", c.options.GetQueueName())
	c.close()
	if c.shutdown != nil {
		close(c.shutdown)
	}

	c.shutdown = nil
	c.State = StateNotListening
}

func (c *Consumer) connect(options *Options) error {
	connection, err := rabbit.Dial(options.URL)
	if err != nil {
		return err
	}

	channel, err := connection.Channel()
	if err != nil {
		return err
	}

	err = channel.Qos(PrefetchCount, PrefetchSize, Global)
	if err != nil {
		return err
	}

	err = ConfigureExchanges(channel, options)
	if err != nil {
		return err
	}

	err = ConfigureQueues(channel, options)
	if err != nil {
		return err
	}

	c.connection = connection
	c.channel = channel
	c.options = options
	return nil
}

func (c *Consumer) close() {
	c.closeChannel()
	c.closeConnection()
}

func (c *Consumer) closeChannel() {
	if c.channel == nil {
		return
	}
	if c.channel.IsClosed() {
		return
	}

	err := c.channel.Close()
	if err != nil {
		c.logger.Errorf("failed to close channel %v", err)
	}
}

func (c *Consumer) closeConnection() {
	if c.connection == nil {
		return
	}
	if c.connection.IsClosed() {
		return
	}

	err := c.connection.Close()
	if err != nil {
		c.logger.Errorf("failed to close connection %s", err)
	}
}

func (c *Consumer) consume() error {
	deliveries, err := c.channel.Consume(
		c.options.GetQueueName(),
		ConsumerName,
		AutoAck,
		Exclusive,
		NoLocal,
		NoWait,
		nil)

	if err != nil {
		return err
	}

	c.State = StateListening
	c.shutdown = make(chan bool)

	go c.handleDeliveries(deliveries)
	go c.monitorConnection()

	c.logger.Infof("started listening for messages in %s", c.options.GetQueueName())
	<-c.shutdown
	return nil
}

func (c *Consumer) monitorConnection() {
	closeErr := <-c.connection.NotifyClose(make(chan *rabbit.Error))
	c.Stop()

	if closeErr != nil {
		c.logger.Errorf("connection closed with error %v", closeErr)
		retryErr := c.retryWithConstantWait("Retry RabbitMQ connection",
			ReconnectionAttempts, ReconnectionDelayDuration, func() error {
				return c.connect(c.options)
			})

		if retryErr == nil {
			c.logger.Infof("Successfully reconnected to RabbitMQ")
			go func() {
				err := c.consume()
				if err != nil {
					c.logger.Errorf("failed to start consuming %v", err)
				}
			}()
			return
		}

		log.Fatalf("Could not reconnect to RabbitMQ - shutting down")
	}
}

func (c *Consumer) handleDeliveries(deliveries <-chan rabbit.Delivery) {
	for delivery := range deliveries {
		err := c.processor(NewDelivery(&delivery))
		if err != nil {
			c.handleError(&delivery, err)
		}
	}
}

func (c *Consumer) handleError(delivery *rabbit.Delivery, err error) {
	log.Print(err.Error())

	value, keyExists := delivery.Headers["retry_count"]
	retryCount, keyIsInteger := value.(int32)
	if keyExists && keyIsInteger {
		if retryCount < c.options.GetRetryLimit() {
			err = c.sendToDelayQueue(retryCount+1, delivery.Body)
		} else {
			err = c.sendToDeadQueue(delivery.Body)
		}
	} else {
		err = c.sendToDelayQueue(1, delivery.Body)
	}

	if err != nil {
		c.nackOnFailureToSend(delivery)
	} else {
		c.ackOnSuccessfulSend(delivery)
	}
}

func (c *Consumer) ackOnSuccessfulSend(delivery *rabbit.Delivery) {
	ackErr := delivery.Ack(false)
	if ackErr != nil {
		c.logger.Errorf("failed to Ack %v on handleError", delivery.Body)
	}
}

func (c *Consumer) nackOnFailureToSend(delivery *rabbit.Delivery) {
	//Nack(false, true) because otherwise the message would just be dropped.
	//the second parameter is requeue.
	nackErr := delivery.Nack(false, true)
	if nackErr != nil {
		c.logger.Errorf("failed to Nack %v on handleError", delivery.Body)
	}
}

func (c *Consumer) sendToDelayQueue(retryCount int32, body []byte) error {
	queueName := c.options.GetDelayQueueName()
	c.logger.Infof("sending message to retry queue %s with retry count of %d", queueName, retryCount)

	params := PublishParams{
		Body:       body,
		Headers:    map[string]interface{}{"retry_count": retryCount},
		AmqpURL:    c.options.URL,
		RoutingKey: queueName,
	}
	return PublishMessage(&params)
}

func (c *Consumer) sendToDeadQueue(body []byte) error {
	queueName := c.options.GetDeadQueueName()
	c.logger.Infof("sending message to dead queue %s", queueName)
	params := PublishParams{
		Body:       body,
		Headers:    nil,
		AmqpURL:    c.options.URL,
		RoutingKey: queueName,
	}
	return PublishMessage(&params)
}

func (c *Consumer) retryWithConstantWait(task string, maxAttempts int, wait time.Duration, f func() error) error {
	for attempt := 1; ; attempt++ {
		err := f()
		if err == nil {
			return nil
		}

		if attempt > maxAttempts {
			return fmt.Errorf("[%s] failed after [%d] attempts - giving up: %v", task, attempt, err)
		}

		c.logger.Infof("[%s] attempt [%d] failed with [%v] - retrying in %s", task, attempt, err, wait)
		time.Sleep(wait)
	}
}
