package main

import (
	"fmt"
	"os"

	"github.com/rudrankriyam/Galaxy-Store-CLI/cmd"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	versionInfo := fmt.Sprintf("%s (commit %s, built %s)", version, commit, date)
	os.Exit(cmd.Run(os.Args[1:], versionInfo))
}
