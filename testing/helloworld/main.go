package main

import (
	"fmt"
	"io"
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
	http.ListenAndServe(":5566", mux2)
}
