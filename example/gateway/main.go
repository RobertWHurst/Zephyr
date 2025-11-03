package main

import (
	"fmt"
	"net/http"

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

	fmt.Println("Creating gateway")
	gateway := zephyr.NewGateway("example-gateway", natstransport.New(natsConn))
	httpServer := &http.Server{
		Addr:    ":8080",
		Handler: gateway,
	}

	fmt.Println("Starting gateway")
	if err := gateway.Start(); err != nil {
		panic(err)
	}

	fmt.Println("Starting http server on :8080")
	if err = httpServer.ListenAndServe(); err != nil {
		panic(err)
	}
}
