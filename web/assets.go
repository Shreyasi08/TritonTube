// Package web contains embedded UI assets so binaries work from any directory.
package web

import "embed"

//go:embed templates/*.html static/*
var Assets embed.FS
