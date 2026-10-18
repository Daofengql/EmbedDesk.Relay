// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"log"
	"os"

	"github.com/Daofengql/EmbedDesk.Relay/internal/relay"
)

func main() {
	if err := relay.Run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}
