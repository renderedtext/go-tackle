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
	_, err := channel.QueueDeclare(options.GetQueueName(), options.GetDurable(), options.GetAutoDeleted(), options.GetExclusive(), NoWait, nil)
	if err != nil {
		return err
	}

	if options.GetEnableDeadQueue() {
		queueOptions := map[string]interface{}{
			"x-message-ttl": DeadLetterTimeout,
		}

		_, err = channel.QueueDeclare(options.GetDeadQueueName(), options.GetDurable(), options.GetAutoDeleted(), options.GetExclusive(), NoWait, queueOptions)
		if err != nil {
			return err
		}
	}

	if options.GetMaxRetries() > 0 {
		retryQueueOptions := map[string]interface{}{
			"x-message-ttl":             options.GetRetryDelay() * 1000,
			"x-dead-letter-exchange":    options.GetServiceExchangeName(),
			"x-dead-letter-routing-key": options.RoutingKey,
		}

		_, err = channel.QueueDeclare(options.GetDelayQueueName(), options.GetDurable(), options.GetAutoDeleted(), options.GetExclusive(), NoWait, retryQueueOptions)
		if err != nil {
			return err
		}
	}

	err = channel.QueueBind(options.GetQueueName(), options.RoutingKey, options.GetServiceExchangeName(), NoWait, nil)
	if err != nil {
		return err
	}

	return nil
}
