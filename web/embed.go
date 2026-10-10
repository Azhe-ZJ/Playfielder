// Package web contains the browser UI; no Node.js process or frontend build is
// required to build or run this application.
package web

import "embed"

//go:embed assets/*
var Files embed.FS
