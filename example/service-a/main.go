package main

import (
	"context"
	"fmt"

	"github.com/RobertWHurst/navaros"
	"github.com/RobertWHurst/zephyr/v2"
	natstransport "github.com/RobertWHurst/zephyr/v2/nats-transport"
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
	if err := service.Listen(context.Background()); err != nil {
		panic(err)
	}
}
