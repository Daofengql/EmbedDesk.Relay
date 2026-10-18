// SPDX-License-Identifier: GPL-3.0-only
// Package licenses provides the unchanged notices shipped with relay binaries.
package licenses

import "embed"

// Files contains the project and Go runtime dependency licenses.
//
//go:embed *.txt *.md
var Files embed.FS
