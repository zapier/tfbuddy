package main

import (
	"fmt"

	"github.com/zapier/tfbuddy/cmd"
)

var (
	GitTag    = ""
	GitCommit = ""
)

func main() {
	fmt.Println("Starting TFBuddy:", GitTag, GitCommit)
	cmd.Execute()
}
