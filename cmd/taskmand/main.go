// Command taskmand is the Taskman daemon: one process, no external
// dependencies, exposing a small localhost HTTP API for the Chrome
// extension. See docs/PLAN.md for the design.
package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/DanielPhillip-Solvti/taskman/internal/api"
	"github.com/DanielPhillip-Solvti/taskman/internal/config"
	"github.com/DanielPhillip-Solvti/taskman/internal/repos"
	"github.com/DanielPhillip-Solvti/taskman/internal/work"
)

// Set via -ldflags "-X main.version=..." by the release workflow; "dev"
// for local builds.
var version = "dev"

func homeDir() string {
	if v := os.Getenv("TASKMAN_HOME"); v != "" {
		return v
	}
	dir, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("taskmand: could not determine home directory: %v", err)
	}
	return filepath.Join(dir, ".taskman")
}

func main() {
	home := homeDir()

	cfg, err := config.NewStore(home)
	if err != nil {
		log.Fatalf("taskmand: config store: %v", err)
	}
	regis, err := repos.NewRegistry(home)
	if err != nil {
		log.Fatalf("taskmand: repo registry: %v", err)
	}
	mgr, err := work.NewManager(home, cfg, regis)
	if err != nil {
		log.Fatalf("taskmand: task manager: %v", err)
	}

	addr := os.Getenv("TASKMAN_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8765"
	}

	srv := api.New(cfg, regis, mgr)
	log.Printf("taskmand %s: listening on %s (home=%s)", version, addr, home)
	if err := http.ListenAndServe(addr, srv.Routes()); err != nil {
		log.Fatalf("taskmand: server: %v", err)
	}
}
