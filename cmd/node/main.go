package main

import (
	"calnet_server/node"
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGINT, syscall.SIGTERM, syscall.SIGABRT, syscall.SIGQUIT)
	defer cancel()
	n := node.NewNode(&node.NodeOpts{
		ControlUrl: "http://127.0.0.1:8080",
		UdpPort:    0,
	})

	if err := n.Login(); err != nil {
		log.Println(err)
		return
	}

	n.Up()
	<-ctx.Done()
	log.Println("context killed")
	n.Down()
}
