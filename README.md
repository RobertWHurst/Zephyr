# Zephyr

<p>
  <img src="zephyr.png" align="right" width="400">
</p>

Zephyr is a microservice framework developed by the TelemetryTV team. It brings
the HTTP protocol right to your services without compromise. This means that
every service you write feels like it's a simple web right at the edge, rather
that behind a complex messaging system.

This illusion is achieved by allowing services to declare their public routes to
a gateway over a transport method such as NATS.

Zephyr provides three important components:

- **Gateway**: Indexes services it finds via the assigned transport, and routes
  HTTP requests to them. Implements go's http.Handler, so it can be used with
  any http server.
- **Service**: Provides a mechanism for services to declare their public routes,
  as well as receive requests via a gateway.
- **Client**: Used to make requests from service to service. Clients are special
  in that they provide a http client like API, and can make requests to routes
  that are not public, and are not routable from a gateway.

All of these components will work provided that the transport associated with
them use a shared network. For example, if using a NATS transport, as long as
all components are connected to the same NATS cluster, they will be able to
discover and communicate with each other.

<br clear="right" />

> **A Note on Navaros:**
>
> Navaros is a router package that Zephyr has been designed to work with. It is
> not required to use Zephyr, as any http.Handler can be used with the service
> and gateway components. However, Navaros has the concept of public and private
> routes built in, thus using it means you do not need to declare you service
> routes manually.
>
> See the [Navaros](https://github.com/RobertWHurst/navaros) repository for more
> information.

## Working with Zephyr

Using Zephyr for your microservice architecture is simple.

You will need at least two services to start. One service we will be a gateway.
The other will be our first api service. The gateway will be the entry point for
all HTTP requests originating from outside the service network. The api service
will register routes with the gateway, and handle requests forwarded to it by
the gateway.

### Creating a Service

We do this using the `zephyr.NewService` function, which takes three arguments:

- The name of the service
- A transport to use for communication
- The handler to use for incoming requests

```go
service := zephyr.NewService("myservice", natstransport.New(natsConn), Handler)
service.Run()
```

A more complete illustration can be found in the example below.

<details>
<summary>Service Example</summary>

This service is a sketch of a simple CRUD service. The aim here is to show you
how Zephyr can be used with any http library, in this case Navaros.

```go
package main

import (
  "fmt"
  "net/http"

  "github.com/nats-io/nats.go"
  "github.com/RobertWHurst/zephyr"
  natstransport "github.com/RobertWHurst/zephyr/nats-transport"
  "github.com/RobertWHurst/navaros"
  "github.com/RobertWHurst/navaros/middleware/json"
)

const natsURL = "nats://localhost:4222"

var Router = navaros.NewRouter()

func init() {
  Router.Use(json.Middleware(nil))

  Router.PublicCreate("/item", createItem)
  Router.PublicGet("/item", getItems)
  Router.PublicGet("/item/:id", getItem)
  Router.PublicUpdate("/item/:id", updateItem)
  Router.PublicDelete("/item/:id", deleteItem)
}

func main() {

  // Connect to the NATS
  conn, err := nats.Connect(natsURL)
  if err != nil {
    fmt.Printf("Failed to connect to NATS: %s\n", err)
    return
  }

  // Create a new service
  service := zephyr.NewService("myservice", natstransport.New(conn), Router)

  // Run the service
  if err := service.Run(); err != nil {
    fmt.Printf("Failed to run service: %s\n", err)
  }
}

type Item struct {
  ID int `json:"id"`
  Name string `json:"name"`
  Description string `json:"description"`
}

idIndex := 1
var items []*Item

func createItem(ctx *navaros.Context) {
  item := &Item{}
  if err := ctx.UnmarshalRequestBody(item); err != nil {
    ctx.Status = http.StatusBadRequest
    return
  }
  
  var fieldErrors []*json.FieldError
  if item.Name == "" {
    fieldErrors = append(fieldErrors, &json.FieldError{
      Field: "name",
      Message: "Name is required",
    })
  }
  if item.Description == "" {
    fieldErrors = append(fieldErrors, &json.FieldError{
      Field: "description",
      Message: "Description is required",
    })
  }
  if len(fieldErrors) != 0 {
    ctx.Body = fieldErrors
    return
  }

  item.ID = idIndex += 1
  items = append(items, item)

  ctx.Status = http.StatusCreated
}

func getItems(ctx *navaros.Context) {
  ctx.Body = items
}

func getItem(ctx *navaros.Context) {
  idStr := ctx.Params().Get("id")
  id, err := strconv.Atoi(idStr)
  if err != nil {
    ctx.Status = http.StatusBadRequest
    return
  }

  var targetItem *Item
  for _, item := range items {
    if item.ID == id {
      targetItem = item
      break
    }
  }

  if targetItem == nil {
    return
  }

  ctx.Body = targetItem
}

func updateItem(ctx *navaros.Context) {
  idStr := ctx.Params().Get("id")
  id, err := strconv.Atoi(idStr)
  if err != nil {
    ctx.Status = http.StatusBadRequest
    return
  }

  var targetItem *Item
  for _, item := range items {
    if item.ID == id {
      targetItem = item
      break
    }
  }

  if targetItem == nil {
    return
  }

  item := &Item{}
  if err := ctx.UnmarshalRequestBody(item); err != nil {
    ctx.Status = http.StatusBadRequest
    return
  }

  if item.Name != "" {
    targetItem.Name = item.Name
  }
  if item.Description != "" {
    targetItem.Description = item.Description
  }

  ctx.Status = http.NoContent
}

```

</details>

### Creating a Gateway

In order for our service to be accessible, we will need a way to get requests
from the outside world to our service. This is where the gateway comes in.

A gateway is a service that can listen for HTTP requests from outside your
service network. It is responsible for routing these requests to the correct
service. Zephyr provides a gateway struct which handles this responsibility.

To create a gateway, we use the `zephyr.NewGateway` function, which takes two
arguments:

- The name of the gateway.
- A transport to use for communication.

The returned gateway can then be used as an http.Handler with go's http package.

```go
gateway := zephyr.NewGateway("mygateway", natstransport.New(natsConn))
http.ListenAndServe(":8080", gateway)
```

<details>
<summary>Gateway Example</summary>

This gateway is a simple example of a gateway that listens for HTTP requests on
port 8080, and forwards them to the service we created in the previous example.

```go
package main

import (
  "fmt"
  "net/http"

  "github.com/nats-io/nats.go"
  "github.com/RobertWHurst/zephyr"
  natstransport "github.com/RobertWHurst/zephyr/nats-transport"
)

const natsURL = "nats://localhost:4222"

func main() {
  // Connect to the NATS
  conn, err := nats.Connect(natsURL)
  if err != nil {
    fmt.Printf("Failed to connect to NATS: %s\n", err)
    return
  }

  // Create a new gateway
  gateway := zephyr.NewGateway("mygateway", natstransport.New(conn))

  // Listen for requests
  if err := http.ListenAndServe(":8080", gateway); err != nil {
    fmt.Printf("Failed to listen and serve: %s\n", err)
  }
}
```

</details>

### More than One Service

If we only wanted to run a single service, we wouldn't need Zephyr in the first
place. At some point, we will want to add more services. These additional
services follow the same pattern outlined earlier. The gateway will automatically
discover these services and route requests to them.

### Making Service to Service Requests

At some point a service will need to make a request to another service. This is
where the zephyr client comes in. It provides a http client like API and allows
you to call non-public routes.

```go
client := zephyr.NewClient(natstransport.New(natsConn))
resp, err := client.Service("myservice").Get("/item/1")
```

<details>
<summary>Client Request Examples</summary>

Here are some examples of how you can use the client to make requests to other
services.

```go
package main

import (
  "bytes"
  "fmt"
  "net/http"

  "github.com/nats-io/nats.go"
  "github.com/RobertWHurst/zephyr"
  natstransport "github.com/RobertWHurst/zephyr/nats-transport"
)

const natsURL = "nats://localhost:4222"

type Item struct {
  ID int `json:"id"`
  Name string `json:"name"`
  Description string `json:"description"`
}

func main() {
  // Connect to the NATS
  conn, err := nats.Connect(natsURL)
  if err != nil {
    fmt.Printf("Failed to connect to NATS: %s\n", err)
    return
  }

  // Create a new client
  client := zephyr.NewClient(natstransport.New(conn))

  // Make a request to the myservice service
  resp1, err := client.Service("myservice").Get("/item/1")
  if err != nil {
    fmt.Printf("Failed to make request: %s\n", err)
    return
  }
  if resp1.StatusCode != http.StatusOK {
    fmt.Printf("Failed to get item: %d\n", resp1.StatusCode)
    return
  }
  fmt.Printf("Response 1: %v\n", resp1)

  newItem := &Item{
    Name: "New Item",
    Description: "A new item",
  }
  resp2, err := client.Service("myservice").Post("/item", newItem)
  if err != nil {
    fmt.Printf("Failed to make request: %s\n", err)
    return
  }
  if resp2.StatusCode != http.StatusCreated {
    fmt.Printf("Failed to create item: %d\n", resp2.StatusCode)
    return
  }
  fmt.Printf("Response 2: %v\n", resp2)

  updatedItem := &Item{
    Name: "Updated Item",
  }
  updatedItemBuf, err := json.Marshal(updatedItem)
  if err != nil {
    fmt.Printf("Failed to marshal item: %s\n", err)
    return
  }
  req3 := http.NewRequest(http.MethodPut, "http://myservice/item/1", bytes.NewReader(updatedItemBuf))
  resp3, err := client.Do(req3)
  if err != nil {
    fmt.Printf("Failed to make request: %s\n", err)
    return
  }
  if resp3.StatusCode != http.StatusNoContent {
    fmt.Printf("Failed to update item: %d\n", resp3.StatusCode)
    return
  }
  fmt.Printf("Response 3: %v\n", resp3)
}
```

</details>
