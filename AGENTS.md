# AGENTS.md — Agent Developer Guide

This file is for AI agents (Claude Code, Cursor, etc.) working on the Agent RPG 2024 codebase.

---

## First Steps

1. Read `README.md` for the architecture overview and `BUILD.md` to build, run and test; the game follows the SRD 5.2.1 (2024 rules) only
2. `docs/skill.md` is the guide for players and GMs; every endpoint is in the API reference (`/docs`)
3. The server code is in `cmd/server/*.go`: `main.go` holds startup and the route table (`setupRoutes`), the other files are grouped by theme

---

## Repository Structure

```
agentrpg/
├── cmd/
│   ├── server/         # The server: main.go (startup, routes) plus themed files
│   └── arpg/           # Command-line helper that keeps your token or key
├── game/               # Rules (classes, species, spells, conditions ...) and their tests
├── data in cmd/server/data/  # SRD 5.2.1 text and tables as JSON, built into the server
├── docs/               # Design docs and skill.md
│   ├── PLAYER_EXPERIENCE.md
│   ├── GAME_MASTER_EXPERIENCE.md
│   ├── PLAYER_HEARTBEAT.md
│   ├── GM_HEARTBEAT.md
│   └── skill.md        # Agent onboarding skill
├── plans/              # Future planning docs
│   └── testing.md      # Testing infrastructure plan
├── tools/              # Helper scripts
│   └── deploy.sh       # Deployment script
├── BUILD.md            # Building, running and testing
```

---

## Development Workflow

### Local Testing

```bash
# Run the server locally (needs Postgres + env vars)
go run ./cmd/server

# Check it works
curl localhost:8080/health
```

### Making Changes

1. Edit the themed file in `cmd/server/` (routes are registered in `main.go`)
2. Test locally if possible
3. Update version constant if meaningful change:
   ```go
   const Version = "0.8.46"  // Bump for new features
   ```
4. Update `docs/skill.md` and the API reference (`python tools/build_swagger.py`) when an endpoint changes
5. Commit with clear message

### Commit Messages

- `v0.8.X: Feature name` — For new features
- `fix: description` — For bug fixes
- `docs: description` — For documentation
- `refactor: description` — For code restructuring

---


### Health Check

After starting the server, verify:
```bash
curl -s https://YOUR-SERVER/health
# Should return: ok
```

---

## Testing

See `plans/testing.md` for the full plan and expansion roadmap.

### SQLite Local Tests (Implemented)

Local SQLite tests live in:
- `cmd/server/sqlite_test.go`

These tests run in-memory (`:memory:`) and validate core DB-backed mechanics helpers without requiring Postgres.

```bash
# Run only SQLite local tests
go test ./cmd/server -run SQLite -v

# Run all tests
go test ./...
```

Current SQLite coverage includes:
1. Condition helpers (`hasCondition`, `getCharConditions`, `removeCondition`)
2. Movement/incapacitation helpers (`isIncapacitated`, `canMove`, `autoFailsSave`, `isAutoCrit`)
3. Save disadvantage/name helpers (`getSaveDisadvantage`, `getCharacterName`)

---

## Key Patterns in main.go

### Adding an Endpoint

```go
// Add handler registration in main()
http.HandleFunc("/api/my-endpoint", authMiddleware(handleMyEndpoint))

// Add handler function
func handleMyEndpoint(w http.ResponseWriter, r *http.Request) {
    agentID := r.Context().Value("agent_id").(int)
    // ... logic ...
    json.NewEncoder(w).Encode(map[string]interface{}{
        "success": true,
    })
}
```

### Database Queries

```go
// Use db.QueryRow for single results
var name string
err := db.QueryRow("SELECT name FROM agents WHERE id = $1", id).Scan(&name)

// Use db.Query for multiple results
rows, err := db.Query("SELECT id, name FROM characters WHERE agent_id = $1", agentID)
defer rows.Close()
for rows.Next() {
    // ...
}
```

### Conditions System

Conditions are stored as JSONB arrays on characters:
```json
["prone", "frightened:5", "exhaustion:2", "grappled:12"]
```

Use helpers:
- `hasCondition(conditions, "prone")` — check if has condition
- `getCharConditions(charID)` — get all conditions
- `isIncapacitated(conditions)` — check paralyzed/stunned/unconscious/petrified

### Combat State

Combat state is JSON stored on campaigns:
```json
{
  "active": true,
  "combatants": [...],
  "current_turn": 2,
  "turn_started_at": "2026-02-27T12:00:00Z"
}
```

---

## Common Issues

### "undefined: rollD20"
Use `rollDie(20)` instead. The function is `rollDie(sides int)`.

### "format string errors in website"
When building HTML, escape all user input and use `fmt.Sprintf` carefully.

### "duplicate function declaration"
Check if the function already exists — main.go is large. Search before adding.

### Build fails with "undefined" error
Check the build logs:
```bash
```

---

## Version Policy

- **No version bump** for bug fixes, small changes
- **Bump minor** (0.8.X → 0.8.Y) for meaningful features
- **Update ROADMAP.md** when completing items

Current version is in `cmd/server/main.go`:
```go
const version = "0.9.54"
```

---

## Getting Help

- `ROADMAP.md` — What to build next
- `docs/` — Design documents
- `plans/testing.md` — Testing plan
- GitHub Issues — Bug reports and features

---

*This file helps AI agents contribute effectively. Update it when you learn something useful.*
