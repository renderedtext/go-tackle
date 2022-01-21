# Tackle

An opinionated RabbitMQ message processing and publishing library, ideal
for async communicaton between microservices.

## Installation

```
go get github.com/renderedtext/go-tackle
```

## Publishing messages to a RabbitMQ exchange (simple)

To publish a message to a RabbitMQ exchange, use the `tackle.PublishMessage`
function. For example, if you are writting a user managment service and want
to publish that a user was created, use the following snippet.

``` golang
package main

import (
  tackle "github.com/renderedtext/go-tackle"
)

func main() {
  publishParams := tackle.PublishParams{
    Body:       []byte(`{"user_id": "123"}`),
    RoutingKey: "user-created",
    Exchange:   "user-exchange",
    AmqpURL:    "guest@localhost:5467",
  }

  err := tackle.PublishMessage(&publishParams)
  if err != nil {
    log.Info("something went wrong while publishing %v", err)
  }
}
```

## Publishing messages to a RabbitMQ exchange (advanced)

In the simple publishing mechanism, tacle will open and close a connection
every time it sends a message. This is fine for sending one or two messages,
however, if you plan to publish large batches of messages, it will be more
efficient to create a dedicated publisher that keeps the connection open
for a longer duration.

``` golang
package main

import (
  tackle "github.com/renderedtext/go-tackle"
)

func main() {
  publisher := tackle.NewPublisher("guest@localhost:5467")

  err := publisher.Connect()
  defer publisher.Close()
  if err != nil {
    log.Info("failed to connect to rabbit mq server %v", err)
  }

  publishParams := tackle.PublishParams{
    Body:       []byte(`{"user_id": "123"}`),
    RoutingKey: "user-created",
    Exchange:   "user-exchange",
    AmqpURL:    "guest@localhost:5467",
  }

  err := publish.Publish(&publishParams))
  if err != nil {
    log.Info("something went wrong while publishing %v", err)
  }
}
```

## Consuming messages from RabbitMQ

To consume messages from rabbit mq, you need to set up a consumer.
Here is an example consumer for the above example messages:

``` golang
package main

import (
  tackle "github.com/renderedtext/go-tackle"
)

func main() {
  consumer := tackle.NewConsumer()

  options := tackle.Options{
    URL:            "amqp://guest:guest@rabbitmq:5672",
    RemoteExchange: "user-exchange",
    Service:        "user-persister",
    RoutingKey:     "user-created",
  }

  consumer.Start(&options, func(delivery Delivery) {
    fmt.Printf("Received message from the consumer: %s", delivery.Body())

    delivery.Ack()
  })
}
```

Let's brake down what happens here: We are connecting to the remote exchange
"user-exchange" and consuming those messages in our local "user-persister" 
queue.

Visually this looks like this:

```
+--------------------+
| Publishing Service |
+--------------------+
       | 
       | Publish 
       | "user-created"
       |           
   +---|---RabbitMQServer-------------------------------------------------------+
   |   v                                                                        |
   | +---------------+                       * defined by publishing service *  |
   | | user-exchange |                                                          |
   | +---------------+                                                          |
   |   |                                                                        |
   |   | key = user-created                                                     |
   |   |                                                                        |
   |---|------------------------------------------------------------------------|
   |   |                                                                        |
   |   |                                     * defined by subscriber service *  |
   |   v                                                                        |
   |  +-------------------------+                                               |
   |  | user-persister-exchange | <-+                                           |
   |  +------*------------------+   |                                           |
   |         |                      | after N secs                              |
   |         v                      |                                           |
   |  +----------------+   +----------------------+    +--------------------+   |
   |  | user-persister |   | user-persister-delay |    | user-perister-dead |   |
   |  +------*---------+   +----------------------+    +--------------------+   |
   |         |                               ^                 ^                |
   +---------|-------------------------------|-----------------|----------------+
             |                               |                 |
             v                               |                 |
       +-------------------+ ----(on err)----+                 |
       | Consuming Service |                                   |
       +-------------------+ ------------------- (after N err)-+
```