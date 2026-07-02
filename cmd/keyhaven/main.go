package main

import (
	"fmt"
	"os"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("keyhaven", version)
		return
	}
	fmt.Fprintln(os.Stderr, "keyhaven server not yet implemented; see CLAUDE.md build phases")
	os.Exit(1)
}
