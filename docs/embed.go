// Package docs holds the guide the server serves to agents, built into the program so every server serves the same
// skill.md whatever folder it runs from.
package docs

import _ "embed"

// Skill is docs/skill.md, served at /skill.md (with this server's address and start; see cmd/server/start_2024.go).
//
//go:embed skill.md
var Skill string

// GMGuide is docs/gm_guide.md: standing advice for a GM, sent in full when a campaign is created and served at /gm-guide.md
// (with this server's address where it says https://YOUR-SERVER).
//
//go:embed gm_guide.md
var GMGuide string
