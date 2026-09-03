// Copyright (c) 2025 JoeGlenn1213
// Echo Plugin - The simplest possible plugin for testing

package echo

import (
	"fmt"

	"github.com/JoeGlenn1213/actiond/internal/event"
	"github.com/JoeGlenn1213/actiond/internal/plugin"
)

// EchoPlugin is a test plugin that prints event details
// "Dumb enough that it can't fail"
type EchoPlugin struct{}

// Name returns the plugin identifier
func (p *EchoPlugin) Name() string {
	return "echo"
}

// Version returns the built-in plugin version (verifier provenance).
func (p *EchoPlugin) Version() string {
	return "builtin"
}

// Triggers returns the event types this plugin responds to
func (p *EchoPlugin) Triggers() []string {
	return []string{event.TypeGitPush, event.TypeRepoAdded}
}

// Languages returns supported languages - echo runs on all projects
func (p *EchoPlugin) Languages() []string {
	return []string{"*"}
}

// Match always returns true - we want to see everything
func (p *EchoPlugin) Match(e event.Event) bool {
	return true
}

// Profiles returns empty - echo is a debug plugin, not included in any profile
func (p *EchoPlugin) Profiles() []string {
	return nil
}

// Run prints the event details and writes to artifacts
func (p *EchoPlugin) Run(ctx plugin.Context) error {
	fmt.Println("🔥 Action triggered!")
	fmt.Println("────────────────────")
	fmt.Printf("  Type:      %s\n", ctx.Event.Type)
	fmt.Printf("  Repo:      %s\n", ctx.Event.Repo)
	fmt.Printf("  Timestamp: %s\n", ctx.Event.Timestamp.Format("2006-01-02 15:04:05"))

	if ctx.Event.Old != "" {
		fmt.Printf("  Old:       %s\n", ctx.Event.Old)
	}
	if ctx.Event.New != "" {
		fmt.Printf("  New:       %s\n", ctx.Event.New)
	}
	if ctx.Event.Replayed {
		fmt.Println("  ⚠️  This is a REPLAYED event")
	}

	fmt.Println("────────────────────")

	// Write artifacts if writer is available
	if ctx.Artifacts != nil {
		// Write meta.json with event details
		meta := map[string]interface{}{
			"plugin":    p.Name(),
			"event_id":  ctx.Event.ID,
			"type":      ctx.Event.Type,
			"repo":      ctx.Event.Repo,
			"timestamp": ctx.Event.Timestamp,
			"replayed":  ctx.Event.Replayed,
		}
		if err := ctx.Artifacts.WriteJSON("meta.json", meta); err != nil {
			return fmt.Errorf("failed to write meta.json: %w", err)
		}

		// Write a simple summary
		summary := fmt.Sprintf("# Echo Action\n\nEvent: %s\nRepo: %s\nTime: %s\n",
			ctx.Event.Type, ctx.Event.Repo, ctx.Event.Timestamp.Format("2006-01-02 15:04:05"))
		if err := ctx.Artifacts.Write("summary.md", []byte(summary)); err != nil {
			return fmt.Errorf("failed to write summary.md: %w", err)
		}
	}

	return nil
}
