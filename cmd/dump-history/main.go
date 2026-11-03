package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	things "github.com/JamesHJung/things-cloud-mcp"
)

func main() {
	username := os.Getenv("THINGS_USERNAME")
	password := os.Getenv("THINGS_PASSWORD")
	if username == "" || password == "" {
		log.Fatal("THINGS_USERNAME and THINGS_PASSWORD must be set")
	}

	client := things.New(things.APIEndpoint, username, password)

	histories, err := client.Histories()
	if err != nil {
		log.Fatalf("failed to list histories: %v", err)
	}

	fmt.Printf("Found %d histories\n\n", len(histories))

	for _, h := range histories {
		fmt.Printf("=== History %s ===\n", h.ID)

		// Read all items from index 0
		req, err := http.NewRequest("GET", fmt.Sprintf("/version/1/history/%s/items?start-index=0", h.ID), nil)
		if err != nil {
			log.Printf("  Error creating request: %v\n", err)
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("  Error fetching items: %v\n", err)
			continue
		}
		bs, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			log.Printf("  Error reading response: %v\n", err)
			continue
		}

		var result struct {
			Items            []json.RawMessage `json:"items"`
			CurrentItemIndex int               `json:"current-item-index"`
			SchemaVersion    int               `json:"schema"`
		}
		if err := json.Unmarshal(bs, &result); err != nil {
			log.Printf("  Error unmarshaling: %v\n", err)
			continue
		}

		fmt.Printf("  Schema: %d, Items: %d, CurrentIndex: %d\n\n", result.SchemaVersion, len(result.Items), result.CurrentItemIndex)

		for i, raw := range result.Items {
			// Each item is a map of UUID -> {t, e, p}
			var itemMap map[string]json.RawMessage
			if err := json.Unmarshal(raw, &itemMap); err != nil {
				fmt.Printf("  [%d] ERROR parsing: %v\n", i, err)
				continue
			}
			for uuid, val := range itemMap {
				var envelope struct {
					T int             `json:"t"`
					E string          `json:"e"`
					P json.RawMessage `json:"p"`
				}
				_ = json.Unmarshal(val, &envelope)

				action := "CREATE"
				switch envelope.T {
				case 1:
					action = "MODIFY"
				case 2:
					action = "DELETE"
				}

				// Pretty print the payload
				var pretty bytes.Buffer
				_ = json.Indent(&pretty, envelope.P, "       ", "  ")

				fmt.Printf("  [%d] %s %s %s\n       %s\n\n", i, action, envelope.E, uuid, pretty.String())
			}
		}
	}
}
