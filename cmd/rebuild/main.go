// Command rebuild repairs a Things sync database in place by replaying
// change_log from server index 0.
//
// It is a one-off repair for databases written before the sync layer handled
// "Task7" deletions and container cascades. change_log already holds every
// payload Things Cloud sent, so the rebuild needs no network access and no
// Things credentials — only the database file.
//
//	rebuild                      # repair the default database
//	rebuild -db /path/to/sync.db # repair a specific database
//	rebuild -diagnose            # report problems, change nothing
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/arthursoares/things-cloud-sdk/sync"
)

// containerDBPath is where server/main.go opens its database inside the
// container image, and therefore where a NAS/Docker deployment keeps it.
const containerDBPath = "/data/things.db"

// resolveDBPath picks the database to operate on, in order of precedence:
// the -db flag, THINGS_DB_PATH, the container path if it exists, then the
// local CLI default. The container fallback means `docker exec <container>
// things-rebuild` needs no arguments.
func resolveDBPath(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if env := os.Getenv("THINGS_DB_PATH"); env != "" {
		return env
	}
	if _, err := os.Stat(containerDBPath); err == nil {
		return containerDBPath
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "sync.db"
	}
	return filepath.Join(home, ".things-workflow", "sync.db")
}

func printDiagnostics(label string, d sync.Diagnostics) {
	fmt.Printf("%s:\n", label)
	fmt.Printf("  rows surviving a logged deletion:         %d\n", d.StaleDeletes)
	fmt.Printf("  untrashed children of trashed containers: %d (of which completed: %d)\n",
		d.OrphanedChildren, d.OrphanedCompleted)
	fmt.Printf("  change_log rows still genuinely unknown:  %d", d.GenuinelyUnknownRows)
	if len(d.UnknownKinds) > 0 {
		fmt.Printf(" (kinds: %s)", strings.Join(d.UnknownKinds, ", "))
	}
	fmt.Println()
	fmt.Printf("  (legacy UnknownChange rows now parseable: %d)\n", d.LegacyUnknownRows)
	fmt.Printf("  unresolved project/heading/area refs:     %d / %d / %d\n",
		d.UnresolvedProject, d.UnresolvedHeading, d.UnresolvedArea)
}

func main() {
	dbPath := flag.String("db", "",
		"Path to the SQLite sync database (default: $THINGS_DB_PATH, else "+
			containerDBPath+" if present, else ~/.things-workflow/sync.db)")
	diagnoseOnly := flag.Bool("diagnose", false, "Report integrity problems without modifying the database")
	deleteAreaContents := flag.Bool("delete-area-contents", false,
		"On AreaDeleted, delete the area's projects and to-dos instead of keeping them "+
			"and clearing their area reference (the default, which is what Things does)")
	flag.Parse()

	path := resolveDBPath(*dbPath)

	if _, err := os.Stat(path); err != nil {
		log.Fatalf("database %s: %v", path, err)
	}

	// A nil client is fine: rebuild and diagnose never touch the network.
	syncer, err := sync.Open(path, nil)
	if err != nil {
		log.Fatalf("opening %s: %v", path, err)
	}
	defer syncer.Close()

	if *deleteAreaContents {
		syncer.SetAreaDeleteMode(sync.AreaDeleteRemovesContents)
	}

	fmt.Printf("Database: %s\n\n", path)

	before, err := syncer.Diagnose()
	if err != nil {
		log.Fatalf("diagnose failed: %v", err)
	}
	printDiagnostics("Before", before)

	if *diagnoseOnly {
		return
	}

	fmt.Println("\nReplaying change_log from index 0...")
	res, err := syncer.Rebuild()
	if err != nil {
		log.Fatalf("rebuild failed: %v", err)
	}

	fmt.Printf("  replayed %d items from %d change_log rows", res.ItemsReplayed, res.LogRows)
	if res.Inferred > 0 {
		fmt.Printf(" (%d envelopes inferred from pre-v5 rows)", res.Inferred)
	}
	fmt.Printf("\n  live rows: %d tasks, %d projects, %d headings, %d areas, %d tags, %d checklist items\n\n",
		res.Tasks, res.Projects, res.Headings, res.Areas, res.Tags, res.ChecklistItems)

	after, err := syncer.Diagnose()
	if err != nil {
		log.Fatalf("post-rebuild diagnose failed: %v", err)
	}
	printDiagnostics("After", after)

	if after.UnresolvedArea > before.UnresolvedArea ||
		after.UnresolvedProject > before.UnresolvedProject ||
		after.UnresolvedHeading > before.UnresolvedHeading {
		fmt.Println("\nNote: the replay restored container references that the previous state had")
		fmt.Println("dropped. Where the referenced container has no create event in the retained")
		fmt.Println("Things Cloud history, that reference cannot be resolved from change_log and is")
		fmt.Println("counted above. The next Sync() will reconcile it if the server still has it.")
	}
}
