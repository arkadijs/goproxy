package main

import (
	"io"
	"log"
	"net"
	"os"
)

func forward(conn net.Conn) {
	fwd, err := net.Dial("tcp", os.Args[2])
	if err != nil {
		log.Printf("Conection failed: %v", err)
		return
	}
	close := func() {
		fwd.Close()
		conn.Close()
	}
	go func() {
		defer close()
		io.Copy(fwd, conn)
	}()
	go func() {
		defer close()
		io.Copy(conn, fwd)
	}()
}

func main() {
	if len(os.Args) != 3 {
		log.Fatalf("Usage: %s listen:port forward:port\n", os.Args[0])
	}

	listener, err := net.Listen("tcp", os.Args[1])
	if err != nil {
		log.Fatalf("Failed to setup tcp listener: %v", err)
	}

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Failed to accept connection: %v", err)
		} else {
			go forward(conn)
		}
	}
}
