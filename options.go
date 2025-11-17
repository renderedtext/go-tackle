package tackle

import "fmt"

const (
	DefaultRetryLimit = 10
	DefaultRetryDelay = 10
)

type Options struct {
	URL            string
	ConnectionName string
	RemoteExchange string
	Service        string
	RoutingKey     string
	RetryDelay     int32
	RetryLimit     int32
	OnDeadFunc     func(Delivery)

	Durable     *bool
	AutoDeleted *bool
	Exclusive   *bool

	MaxRetries      *int32
	EnableDeadQueue *bool
}

func (o *Options) GetServiceExchangeName() string {
	return fmt.Sprintf("%s.%s", o.Service, o.RoutingKey)
}

func (o *Options) GetQueueName() string {
	return fmt.Sprintf("%s.%s", o.Service, o.RoutingKey)
}

func (o *Options) GetConnectionName() string {
	if o.ConnectionName != "" {
		return o.ConnectionName
	}
	return ConsumerName
}

func (o *Options) GetDeadQueueName() string {
	return fmt.Sprintf("%s.dead", o.GetQueueName())
}

func (o *Options) GetRetryDelay() int32 {
	if o.RetryDelay > 0 {
		return o.RetryDelay
	}
	return DefaultRetryDelay
}

func (o *Options) GetRetryLimit() int32 {
	if o.RetryLimit > 0 {
		return o.RetryLimit
	}
	return DefaultRetryLimit

}

func (o *Options) GetDelayQueueName() string {
	return fmt.Sprintf("%s.delay.%d", o.GetQueueName(), o.GetRetryDelay())
}

func (o *Options) GetDurable() bool {
	if o.Durable != nil {
		return *o.Durable
	}
	return true
}

func (o *Options) GetAutoDeleted() bool {
	if o.AutoDeleted != nil {
		return *o.AutoDeleted
	}
	return false
}

func (o *Options) GetExclusive() bool {
	if o.Exclusive != nil {
		return *o.Exclusive
	}
	return false
}

func (o *Options) GetMaxRetries() int32 {
	if o.MaxRetries != nil {
		return *o.MaxRetries
	}
	return o.GetRetryLimit()
}

func (o *Options) GetEnableDeadQueue() bool {
	if o.EnableDeadQueue != nil {
		return *o.EnableDeadQueue
	}
	return true
}
