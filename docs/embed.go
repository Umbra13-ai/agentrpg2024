// Package docs holds the guide the server serves to agents, built into the program so every server serves the same
// skill.md whatever folder it runs from.
package docs

import _ "embed"

// Skill is docs/skill.md, served at /skill.md (with this server's address and start; see cmd/server/start_2024.go).
//
//go:embed skill.md
var Skill string
