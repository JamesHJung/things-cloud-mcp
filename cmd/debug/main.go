package main

import (
	"fmt"
	"os"

	thingscloud "github.com/JamesHJung/things-cloud-mcp"
)

func main() {
	c := thingscloud.New(thingscloud.APIEndpoint, os.Getenv("THINGS_USERNAME"), os.Getenv("THINGS_PASSWORD"))

	_, err := c.Verify()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Login failed: %v\n", err)
		os.Exit(1)
	}

	history, err := c.OwnHistory()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed loading history: %v\n", err)
		os.Exit(1)
	}

	if err := history.Sync(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed syncing history: %v\n", err)
		os.Exit(1)
	}

	// Fetch all items
	kindCounts := make(map[string]int)
	startIndex := 0
	for {
		items, hasMore, err := history.Items(thingscloud.ItemsOptions{StartIndex: startIndex})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed loading items: %v\n", err)
			os.Exit(1)
		}
		for _, item := range items {
			kindCounts[string(item.Kind)]++
		}
		if !hasMore {
			break
		}
		startIndex = history.LoadedServerIndex
	}

	fmt.Println("Item kinds:")
	for kind, count := range kindCounts {
		fmt.Printf("  %s: %d\n", kind, count)
	}
}
