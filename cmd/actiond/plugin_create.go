package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var (
	pluginName     string
	pluginLanguage string
	pluginPath     string
)

func newPluginCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create [name]",
		Short: "Create a new ActionD plugin scaffold",
		Long: `Create a new ActionD plugin scaffold with boilerplate code.

This command generates a complete plugin structure including:
  • manifest.json - Plugin metadata and configuration
  • run.py/run.sh/run.go - Plugin entry point (based on language)
  • README.md - Plugin documentation template

Supported languages:
  • python (default) - Python 3 with JSON stdin/stdout protocol
  • shell            - Bash/POSIX shell script
  • go               - Go binary plugin

Examples:
  # Create Python plugin in current directory
  $ actiond plugins create my-plugin

  # Create shell plugin in specific path
  $ actiond plugins create deploy-script --lang=shell --path=./my-plugins

  # Create Go plugin
  $ actiond plugins create go-builder --lang=go`,
		Args: cobra.MaximumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			name := pluginName
			if len(args) > 0 {
				name = args[0]
			}
			if name == "" {
				fmt.Println("Error: plugin name is required")
				fmt.Println("\nUsage: actiond plugins create [name] [--lang=language] [--path=path]")
				os.Exit(1)
			}

			if !isValidPluginName(name) {
				fmt.Printf("Error: invalid plugin name '%s'\n", name)
				fmt.Println("   Plugin names should be lowercase with hyphens (e.g., my-plugin, go-lint)")
				os.Exit(1)
			}

			lang := strings.ToLower(pluginLanguage)
			if !isValidLanguage(lang) {
				fmt.Printf("Error: unsupported language '%s'\n", lang)
				fmt.Println("   Supported languages: python, shell, go")
				os.Exit(1)
			}

			targetPath := pluginPath
			if targetPath == "" {
				targetPath = "."
			}

			pluginDir := filepath.Join(targetPath, name)
			if err := os.MkdirAll(pluginDir, 0755); err != nil {
				fmt.Printf("Error creating directory: %v\n", err)
				os.Exit(1)
			}

			if err := generatePluginFiles(pluginDir, name, lang); err != nil {
				fmt.Printf("Error generating plugin files: %v\n", err)
				os.Exit(1)
			}

			fmt.Printf("Plugin '%s' created successfully!\n", name)
			fmt.Printf("\nLocation: %s\n", pluginDir)
			fmt.Println("\nNext steps:")
			fmt.Printf("  1. cd %s\n", pluginDir)
			fmt.Println("  2. Edit manifest.json to configure triggers and metadata")
			fmt.Println("  3. Implement your logic in the run file")
			fmt.Println("  4. Test locally: cat test-input.json | ./run.py")
			fmt.Println("  5. Copy to ActionD plugins directory when ready")
		},
	}

	cmd.Flags().StringVarP(&pluginName, "name", "n", "", "Plugin name (can also be provided as argument)")
	cmd.Flags().StringVarP(&pluginLanguage, "lang", "l", "python", "Plugin language (python, shell, go)")
	cmd.Flags().StringVarP(&pluginPath, "path", "p", "", "Target path for plugin creation (default: current directory)")

	return cmd
}

func isValidPluginName(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' && c != '_' {
			return false
		}
	}
	return true
}

func isValidLanguage(lang string) bool {
	switch lang {
	case "python", "shell", "go":
		return true
	}
	return false
}

func generatePluginFiles(pluginDir, name, lang string) error {
	manifest := generateManifest(name, lang)
	manifestPath := filepath.Join(pluginDir, "manifest.json")
	if err := os.WriteFile(manifestPath, []byte(manifest), 0644); err != nil {
		return fmt.Errorf("failed to write manifest.json: %w", err)
	}

	var runFileName, runContent string
	switch lang {
	case "python":
		runFileName = "run.py"
		runContent = generatePythonPlugin(name)
	case "shell":
		runFileName = "run.sh"
		runContent = generateShellPlugin(name)
	case "go":
		runFileName = "run.go"
		runContent = generateGoPlugin(name)
	}

	runPath := filepath.Join(pluginDir, runFileName)
	if err := os.WriteFile(runPath, []byte(runContent), 0755); err != nil {
		return fmt.Errorf("failed to write %s: %w", runFileName, err)
	}

	readme := generateReadme(name, lang)
	readmePath := filepath.Join(pluginDir, "README.md")
	if err := os.WriteFile(readmePath, []byte(readme), 0644); err != nil {
		return fmt.Errorf("failed to write README.md: %w", err)
	}

	return nil
}

func generateManifest(name, lang string) string {
	runFile := "run.py"
	switch lang {
	case "shell":
		runFile = "run.sh"
	case "go":
		runFile = "run.go"
	}

	return fmt.Sprintf(`{
  "apiVersion": "actiond.dev/v1",
  "name": "%s",
  "version": "1.0.0",
  "description": "TODO: Add a description for your plugin",
  "command": "%s",
  "args": [],
  "triggers": ["git.push"],
  "languages": ["go", "python", "javascript"],
  "timeout": "5m",
  "artifacts": [],
  "enabled": false
}
`, name, runFile)
}

func generatePythonPlugin(name string) string {
	return `#!/usr/bin/env python3
"""` + name + ` - ActionD Plugin

This plugin implements the ActionD plugin protocol using JSON over stdin/stdout.
Communication flow:
1. ActionD sends a JSON payload to stdin with context (repo_path, artifact_dir, etc.)
2. Plugin reads and parses the input
3. Plugin performs its task (linting, testing, building, etc.)
4. Plugin writes a JSON result to stdout with status, output, and artifacts

To customize this plugin:
- Modify the process() function to implement your specific logic
- Update manifest.json to reflect correct triggers and metadata
- Add command-line dependencies or requirements.txt if needed
- Set "enabled": true in manifest.json when ready

For testing locally:
    echo '{"repo_path": "/path/to/repo", "artifact_dir": "/tmp/out"}' | ./run.py
"""

import sys
import json
import os
from pathlib import Path


def process(input_data: dict) -> dict:
    """
    Main processing function. Implement your plugin logic here.

    Args:
        input_data: Dictionary containing:
            - repo_path: Path to the repository being processed
            - artifact_dir: Directory where artifacts should be saved (optional)
            - metadata: Additional context from ActionD (optional)

    Returns:
        Dictionary with required fields:
            - status: "success" | "error"
            - output: Human-readable summary of results
        Optional fields:
            - artifacts: List of artifact filenames created
            - warnings: List of warning messages
            - errors: List of error details
    """
    repo_path = input_data.get("repo_path", ".")
    artifact_dir = input_data.get("artifact_dir")

    # TODO: Implement your plugin logic here
    # Examples:
    # - Run linting tools and capture output
    # - Execute tests and parse results
    # - Build artifacts and save to artifact_dir
    # - Analyze code and generate reports

    result = {
        "status": "success",
        "output": "Plugin executed successfully (TODO: implement actual logic)",
        "artifacts": []
    }

    # Example: Save an artifact if artifact_dir is provided
    if artifact_dir:
        os.makedirs(artifact_dir, exist_ok=True)
        # artifact_path = os.path.join(artifact_dir, "result.json")
        # with open(artifact_path, "w") as f:
        #     json.dump({"results": []}, f)
        # result["artifacts"].append("result.json")

    return result


def main():
    """
    Entry point - handles JSON I/O protocol with ActionD.
    You should not need to modify this function in most cases.
    """
    try:
        # Read input from stdin (ActionD sends JSON context here)
        input_data = json.load(sys.stdin)
    except json.JSONDecodeError as e:
        # Return structured error for invalid input
        result = {
            "status": "error",
            "output": f"Invalid JSON input: {e}",
            "errors": [{"type": "parse_error", "message": str(e)}]
        }
        print(json.dumps(result))
        sys.exit(1)

    # Execute main processing logic
    result = process(input_data)

    # Write result as JSON to stdout (ActionD reads this)
    print(json.dumps(result))

    # Exit with appropriate code
    sys.exit(0 if result.get("status") == "success" else 1)


if __name__ == "__main__":
    main()
`
}

func generateShellPlugin(name string) string {
	return `#!/usr/bin/env bash
#
# ` + name + ` - ActionD Plugin (Shell Version)
#
# This plugin implements the ActionD plugin protocol using JSON over stdin/stdout.
# Communication flow:
# 1. ActionD sends a JSON payload to stdin with context
# 2. Plugin reads and parses the input using jq
# 3. Plugin performs its task
# 4. Plugin writes a JSON result to stdout
#
# Requirements:
# - jq must be installed for JSON parsing
# - All output should be valid JSON to stdout
# - Logs/debug output should go to stderr
#
# To customize:
# - Modify the process() function
# - Update manifest.json with correct triggers
# - Set "enabled": true when ready
#
# For testing locally:
#     echo '{"repo_path": "/path/to/repo"}' | ./run.sh

set -euo pipefail

# Check if jq is available
if ! command -v jq &> /dev/null; then
    echo '{"status": "error", "output": "jq is required but not installed"}' >&2
    exit 1
fi

# Read input from stdin
INPUT=$(cat)

# Extract fields from input using jq
REPO_PATH=$(echo "$INPUT" | jq -r '.repo_path // "."')
ARTIFACT_DIR=$(echo "$INPUT" | jq -r '.artifact_dir // empty')

# TODO: Implement your plugin logic here
# The following is a template structure:

process() {
    local repo_path="$1"
    local artifact_dir="$2"

    # Example: Change to repo directory
    cd "$repo_path" || {
        echo '{"status": "error", "output": "Failed to enter repo directory"}'
        return 1
    }

    # Example: Check for specific files
    if [[ -f "Makefile" ]]; then
        # Run make command
        if make check 2>/dev/null; then
            status="success"
            output="Makefile check passed"
        else
            status="error"
            output="Makefile check failed"
        fi
    else
        status="success"
        output="No Makefile found, nothing to do"
    fi

    # Example: Create artifact if directory provided
    if [[ -n "$artifact_dir" && -d "$artifact_dir" ]]; then
        echo "{\"results\": []}" > "$artifact_dir/result.json"
        artifacts='["result.json"]'
    else
        artifacts="[]"
    fi

    # Output JSON result
    jq -n \
        --arg status "$status" \
        --arg output "$output" \
        --argjson artifacts "$artifacts" \
        '{status: $status, output: $output, artifacts: $artifacts}'
}

# Execute and output result
process "$REPO_PATH" "$ARTIFACT_DIR"
`
}

func generateGoPlugin(name string) string {
	return `package main

/*
` + name + ` - ActionD Plugin (Go Version)

This plugin implements the ActionD plugin protocol using JSON over stdin/stdout.
Communication flow:
1. ActionD sends a JSON payload to stdin with context
2. Plugin reads and parses the input
3. Plugin performs its task
4. Plugin writes a JSON result to stdout

To customize:
- Modify the Input and Result structs if needed
- Implement logic in the process() function
- Update manifest.json with correct triggers
- Build: go build -o run run.go
- Set "enabled": true when ready

For testing locally:
    echo '{"repo_path": "/path/to/repo"}' | go run run.go
*/

import (
	"encoding/json"
	"fmt"
	"os"
)

// Input represents the data sent by ActionD to the plugin
type Input struct {
	RepoPath    string            ` + "`json:\"repo_path\"`" + `              // Path to the repository
	ArtifactDir string            ` + "`json:\"artifact_dir,omitempty\"`" + ` // Directory for artifacts
	Metadata    map[string]string ` + "`json:\"metadata,omitempty\"`" + `     // Additional context
}

// Result represents the response structure expected by ActionD
type Result struct {
	Status    string   ` + "`json:\"status\"`" + `             // "success" or "error"
	Output    string   ` + "`json:\"output\"`" + `             // Human-readable summary
	Artifacts []string ` + "`json:\"artifacts,omitempty\"`" + ` // List of created artifact files
	Errors    []Error  ` + "`json:\"errors,omitempty\"`" + `   // Detailed error information
}

// Error represents a structured error message
type Error struct {
	Type    string ` + "`json:\"type\"`" + `    // Error category
	Message string ` + "`json:\"message\"`" + ` // Error description
}

func main() {
	// Read input from stdin
	var input Input
	decoder := json.NewDecoder(os.Stdin)
	if err := decoder.Decode(&input); err != nil {
		result := Result{
			Status: "error",
			Output: fmt.Sprintf("Failed to parse input: %v", err),
			Errors: []Error{{Type: "parse_error", Message: err.Error()}},
		}
		outputResult(result)
		os.Exit(1)
	}

	// Process and output result
	result := process(input)
	outputResult(result)

	if result.Status != "success" {
		os.Exit(1)
	}
}

// outputResult writes the result as JSON to stdout
func outputResult(result Result) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	encoder.Encode(result)
}

// process implements the main plugin logic
// TODO: Customize this function for your specific use case
func process(input Input) Result {
	// Set defaults
	repoPath := input.RepoPath
	if repoPath == "" {
		repoPath = "."
	}

	// TODO: Implement your logic here
	// Examples:
	// - Run external commands using os/exec
	// - Parse configuration files
	// - Generate reports and save to ArtifactDir
	// - Analyze code structure

	// Example result
	result := Result{
		Status:    "success",
		Output:    "Plugin executed successfully (TODO: implement actual logic)",
		Artifacts: []string{},
	}

	// Example: Create artifact if directory provided
	if input.ArtifactDir != "" {
		// os.MkdirAll(input.ArtifactDir, 0755)
		// artifactPath := filepath.Join(input.ArtifactDir, "result.json")
		// ... write artifact ...
		// result.Artifacts = append(result.Artifacts, "result.json")
	}

	return result
}
`
}

func generateReadme(name, lang string) string {
	runFile := "run.py"
	switch lang {
	case "shell":
		runFile = "run.sh"
	case "go":
		runFile = "run.go"
	}

	return `# ` + name + ` ActionD Plugin

## Overview

TODO: Add a brief description of what this plugin does.

## Structure

- **manifest.json** - Plugin metadata and configuration
- **` + runFile + `** - Plugin entry point
- **README.md** - This file

## Configuration

Edit ` + "`manifest.json`" + ` to customize:

` + "```json\n" + `{
  "name": "` + name + `",
  "description": "Your plugin description here",
  "triggers": ["git.push"],
  "enabled": false
}
` + "```\n\n" + `## Development

### Local Testing

Create a test input file:

` + "```json\n" + `{
  "repo_path": "/path/to/test/repo",
  "artifact_dir": "/tmp/test-artifacts"
}
` + "```\n\n" + `Run the plugin:

` + "```bash\n" + `cat test-input.json | ./` + runFile + `
` + "```\n\n" + `### Iteration Workflow

1. Edit ` + "`" + runFile + "`" + ` to implement your logic
2. Test locally with sample input
3. Update ` + "`manifest.json`" + ` metadata
4. Copy plugin directory to ActionD plugins folder
5. Enable in ActionD: ` + "`actiond plugins enable `" + name + "`\n\n" + `## Deployment

Copy this directory to the ActionD plugins location:

` + "```bash\n" + `cp -r ` + name + ` /path/to/actiond/plugins/
` + "```\n\n" + `Or register via CLI (if supported by your ActionD version).

## Troubleshooting

- Check that the run file has execute permissions: ` + "`chmod +x `" + runFile + "`\n" + `- Verify JSON output is valid (use ` + "`jq`" + ` to validate)
- Review ActionD logs for execution errors
`
}
