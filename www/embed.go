// SPDX-License-Identifier: GPL-3.0-only
// Package consoleui provides the built console files embedded in the relay.
package consoleui

import "embed"

// Files contains a placeholder in source checkouts; release builds first run npm build.
//
//go:embed all:dist
var Files embed.FS
