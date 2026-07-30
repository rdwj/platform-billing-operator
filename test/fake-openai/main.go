package main

import (
	"flag"
	"log"
	"net/http"
)

func main() {
	addr := flag.String("addr", ":8090", "listen address")
	flag.Parse()
	log.Printf("fake-openai listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, newMux()))
}
