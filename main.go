package main

import (
	"fmt"

	"github.com/zapier/tfbuddy/cmd"
	"github.com/zapier/tfbuddy/pkg"
)

func main() {
	fmt.Println("Starting TFBuddy:", pkg.GitTag, pkg.GitCommit)
	cmd.Execute()
}
