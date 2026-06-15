// SPDX-License-Identifier: Apache-2.0

// Package web bundles threadwatch's HTML templates and static assets into
// the binary via go:embed.
package web

import "embed"

// Templates exposes the HTML templates as a sub-FS rooted at templates/.
//
//go:embed templates/*.html
var Templates embed.FS

// Static exposes the bundled CSS/JS as a sub-FS rooted at static/.
//
//go:embed static/*
var Static embed.FS
