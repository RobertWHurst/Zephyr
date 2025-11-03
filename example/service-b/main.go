package main

import (
	"fmt"

	"github.com/RobertWHurst/navaros"
	"github.com/RobertWHurst/zephyr"
	natstransport "github.com/RobertWHurst/zephyr/nats-transport"
	"github.com/nats-io/nats.go"
)

func main() {
	fmt.Println("Connecting to nats")
	natsConn, err := nats.Connect("nats://localhost:4222")
	if err != nil {
		panic(err)
	}
	transport := natstransport.New(natsConn)

	fmt.Println("Creating router")
	router := navaros.NewRouter()

	fmt.Println("Creating client")
	client := zephyr.NewClient(transport)

	fmt.Println("Binding client to router")
	router.PublicGet("/leap", client.Service("example-service-a"))

	fmt.Println("Creating service")
	service := zephyr.NewService("example-service-b", transport, router)
	if err := service.Start(); err != nil {
		panic(err)
	}

	channel := make(chan struct{})
	<-channel
}
