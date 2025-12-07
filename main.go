package main

import (
	"log"
)

func main() {
	log.Fatal(ListenAndServe(":9000"))
}
