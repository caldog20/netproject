package main

import (
	"calnet_server/node"
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
)

func main() {

	conn, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(conn.LocalAddr().String())

	os.Exit(0)

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
