// Copyright (c) 2025 JoeGlenn1213
// ActionD - embedded builtin plugin bundle
// Licensed under MIT

// Package actiond anchors the module root and carries the builtin plugin
// bundle inside the binary (go:embed only sees files under the package's
// own directory, so the embed lives here at the repo root next to plugins/).
//
// Embedding is what gives binary-only installs (Homebrew, release download,
// go install) the same out-of-the-box checks as a source checkout:
// "actiond setup" materializes this bundle into ~/.localgithub/plugins.
// Without it, a fresh brew install ships zero plugins and every push ran no
// checks at all (open-source direction review 2026-09-13, P1 install gap).
package actiond

import "embed"

// PluginsFS holds the repository's plugins/ tree. Files starting with "." or
// "_" (e.g. __pycache__, .DS_Store) are excluded by the default embed rules.
// Directories without a manifest.json (e.g. common/, echo/) are embedded
// harmlessly and filtered out at materialization time (internal/app).
//
//go:embed plugins
var PluginsFS embed.FS
