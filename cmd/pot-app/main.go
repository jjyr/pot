//go:build darwin

package main

import (
	"log"

	"github.com/jjyr/pot/internal/desktop"
)

func main() {
	if err := desktop.Run(); err != nil {
		log.Fatal(err)
	}
}
