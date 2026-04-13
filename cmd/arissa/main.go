// Command arissa is the entrypoint for the arissa Slack agent.
package main

import (
	"fmt"

	"arissa/internal/version"
)

func main() {
	fmt.Printf("[arissa] %s -- scaffold\n", version.Version)
}
