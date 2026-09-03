package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/JoeGlenn1213/actiond/internal/event"
	"github.com/JoeGlenn1213/actiond/internal/plugin"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run test_runner.go <plugin_type>")
		os.Exit(1)
	}

	pluginType := os.Args[1]

	cfg := plugin.ExecPluginConfig{
		Name:    "test-plugin",
		Command: "python3",
		Timeout: 2 * time.Second, // Short timeout for testing
	}

	switch pluginType {
	case "faulty":
		cfg.Args = []string{"faulty_plugin.py"}
	case "timeout":
		cfg.Args = []string{"timeout_plugin.py"}
	default:
		fmt.Println("Unknown plugin type")
		os.Exit(1)
	}

	p := plugin.NewExecPlugin(cfg)

	ctx := plugin.Context{
		Ctx: context.Background(),
		Event: event.Event{
			ID:   "test-event",
			Type: "test.event",
			Repo: "test-repo",
		},
		LogWriter: func(line string, isError bool) {
			prefix := "OUT"
			if isError {
				prefix = "ERR"
			}
			fmt.Printf("[%s] %s\n", prefix, line)
		},
	}

	fmt.Printf("Running %s plugin...\n", pluginType)
	err := p.Run(ctx)
	if err != nil {
		fmt.Printf("Plugin finished with error: %v\n", err)
	} else {
		fmt.Println("Plugin finished successfully")
	}
}
