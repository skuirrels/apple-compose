// Command apple-docker is a Docker CLI front end for Apple's container
// runtime. Alias it as `docker` to keep existing scripts working.
package main

import (
	"os"

	"github.com/skuirrels/apple-compose/internal/dockercli"
)

// version is set at build time.
var version = "dev"

func main() {
	os.Exit(dockercli.Execute(version, os.Args[1:]))
}
