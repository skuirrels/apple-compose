// Command apple-compose runs Compose files on Apple's container runtime.
package main

import (
	"os"

	"github.com/skuirrels/apple-compose/internal/cli"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(cli.Execute(version, os.Args[1:]))
}
