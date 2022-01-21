package tackle

import rabbit "github.com/rabbitmq/amqp091-go"

const (
	DeadLetterTimeout = 604_800_000 // 1 week
)

func ConfigureExchanges(channel *rabbit.Channel, options *Options) error {
	remoteExch := options.RemoteExchange
	serviceExch := options.GetServiceExchangeName()

	err := channel.ExchangeDeclare(remoteExch, "direct", Durable, AutoDeleted, Internal, NoWait, nil)
	if err != nil {
		return err
	}

	err = channel.ExchangeDeclare(serviceExch, "direct", Durable, AutoDeleted, Internal, NoWait, nil)
	if err != nil {
		return err
	}

	err = channel.ExchangeBind(serviceExch, options.RoutingKey, options.RemoteExchange, NoWait, nil)
	if err != nil {
		return err
	}

	return nil
}

func ConfigureQueues(channel *rabbit.Channel, options *Options) error {
	queueOptions := map[string]interface{}{
		"x-message-ttl": DeadLetterTimeout,
	}

	retryQueueOptions := map[string]interface{}{
		"x-message-ttl":             options.GetRetryDelay() * 1000,
		"x-dead-letter-exchange":    options.GetServiceExchangeName(),
		"x-dead-letter-routing-key": options.RoutingKey,
	}

	_, err := channel.QueueDeclare(options.GetQueueName(), Durable, AutoDeleted, Exclusive, NoWait, nil)
	if err != nil {
		return err
	}

	_, err = channel.QueueDeclare(options.GetDeadQueueName(), Durable, AutoDeleted, Exclusive, NoWait, queueOptions)
	if err != nil {
		return err
	}

	_, err = channel.QueueDeclare(options.GetDelayQueueName(), Durable, AutoDeleted, Exclusive, NoWait, retryQueueOptions)
	if err != nil {
		return err
	}

	err = channel.QueueBind(options.GetQueueName(), options.RoutingKey, options.GetServiceExchangeName(), NoWait, nil)
	if err != nil {
		return err
	}

	return nil
}
