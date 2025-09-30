package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "Hello from port 5555")
	})
	fmt.Println("listening on port 5555")
	go http.ListenAndServe(":5555", mux)

	mux2 := http.NewServeMux()
	mux2.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "Hello from port 5566")
	})
	fmt.Println("listening on port 5566")
	go http.ListenAndServe(":5566", mux2)

	// TCP listener on port 5577
	go func() {
		ln, err := net.Listen("tcp", ":5577")
		if err != nil {
			fmt.Println("Error starting TCP listener:", err)
			return
		}
		fmt.Println("listening on TCP port 5577")
		for {
			conn, err := ln.Accept()
			if err != nil {
				fmt.Println("Error accepting connection:", err)
				continue
			}
			go func(c net.Conn) {
				defer c.Close()
				c.Write([]byte("hello world\n"))
			}(conn)
		}
	}()

	// UDP listener on port 5578
	go func() {
		addr, err := net.ResolveUDPAddr("udp", ":5578")
		if err != nil {
			fmt.Println("Error resolving UDP address:", err)
			return
		}
		conn, err := net.ListenUDP("udp", addr)
		if err != nil {
			fmt.Println("Error starting UDP listener:", err)
			return
		}
		defer conn.Close()
		fmt.Println("listening on UDP port 5578")
		buffer := make([]byte, 1024)
		for {
			_, clientAddr, err := conn.ReadFromUDP(buffer)
			if err != nil {
				fmt.Println("Error reading from UDP:", err)
				continue
			}
			response := []byte("hello world\n")
			_, err = conn.WriteToUDP(response, clientAddr)
			if err != nil {
				fmt.Println("Error writing to UDP:", err)
			}
		}
	}()

	// Block forever
	select {}
}
