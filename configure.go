package tackle

import rabbit "github.com/rabbitmq/amqp091-go"

const (
	DeadLetterTimeout = 604_800_000 // 1 week
)

func ConfigureExchanges(channel *rabbit.Channel, options *Options) error {
	err := channel.ExchangeDeclare(options.RemoteExchange, "direct", Durable, AutoDeleted,
		Internal, NoWait, nil)
	if err != nil {
		return err
	}

	err = channel.ExchangeDeclare(options.GetServiceExchangeName(), "direct", Durable, AutoDeleted,
		Internal, NoWait, nil)
	if err != nil {
		return err
	}

	err = channel.ExchangeBind(options.GetServiceExchangeName(), options.RoutingKey, options.RemoteExchange,
		NoWait, nil)
	if err != nil {
		return err
	}

	return nil
}

func ConfigureQueues(channel *rabbit.Channel, options *Options) error {
	_, err := channel.QueueDeclare(options.GetQueueName(), Durable, AutoDeleted, Exclusive, NoWait, nil)
	if err != nil {
		return err
	}

	_, err = channel.QueueDeclare(options.GetDeadQueueName(), Durable, AutoDeleted, Exclusive, NoWait,
		map[string]interface{}{
			"x-message-ttl": DeadLetterTimeout,
		})

	if err != nil {
		return err
	}

	_, err = channel.QueueDeclare(options.GetDelayQueueName(), Durable, AutoDeleted, Exclusive, NoWait,
		map[string]interface{}{
			"x-message-ttl":             options.GetRetryDelay() * 1000,
			"x-dead-letter-exchange":    options.GetServiceExchangeName(),
			"x-dead-letter-routing-key": options.RoutingKey,
		})

	if err != nil {
		return err
	}

	err = channel.QueueBind(options.GetQueueName(), options.RoutingKey, options.GetServiceExchangeName(),
		NoWait, nil)
	if err != nil {
		return err
	}

	return nil
}
