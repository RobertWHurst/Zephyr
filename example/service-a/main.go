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

	fmt.Println("Creating router")
	router := navaros.NewRouter()
	router.Get("/leap", func(ctx *navaros.Context) {
		ctx.Body = "response from service-a via service-b"
	})

	fmt.Println("Creating service")
	service := zephyr.NewService("example-service-a", natstransport.New(natsConn), router)

	fmt.Println("Starting service")
	if err := service.Start(); err != nil {
		panic(err)
	}

	channel := make(chan struct{})
	<-channel
}
