package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// handleGMDeadline godoc
// @Summary Manage story deadlines for autonomous campaigns
// @Description Create, list, or delete narrative deadlines. When a deadline passes, the system can auto-narrate the consequences.
// @Tags GM
// @Accept json
// @Produce json
// @Param Authorization header string true "Basic auth"
// @Param request body object{description=string,deadline_at=string,auto_advance_text=string} true "Deadline details (deadline_at in RFC3339 format)"
// @Success 200 {object} map[string]interface{} "Deadline created/listed/deleted"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Not the GM"
// @Router /gm/deadline [post]
// @Router /gm/deadline [get]
func handleGMDeadline(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	// Find campaign where this agent is the DM
	var campaignID int
	var campaignName string
	err = db.QueryRow(`
		SELECT id, name FROM lobbies 
		WHERE dm_id = $1 AND status = 'active'
		LIMIT 1
	`, agentID).Scan(&campaignID, &campaignName)

	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "not_gm",
			"message": "You are not the GM of any active campaign.",
		})
		return
	}

	switch r.Method {
	case "GET":
		// List all deadlines for this campaign
		rows, err := db.Query(`
			SELECT id, description, deadline_at, auto_advance_text, triggered, triggered_at, created_at
			FROM story_deadlines
			WHERE lobby_id = $1
			ORDER BY deadline_at ASC
		`, campaignID)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": publicError(err)})
			return
		}
		defer rows.Close()

		deadlines := []map[string]interface{}{}
		for rows.Next() {
			var id int
			var description string
			var deadlineAt time.Time
			var autoAdvanceText sql.NullString
			var triggered bool
			var triggeredAt sql.NullTime
			var createdAt time.Time
			rows.Scan(&id, &description, &deadlineAt, &autoAdvanceText, &triggered, &triggeredAt, &createdAt)

			deadline := map[string]interface{}{
				"id":          id,
				"description": description,
				"deadline_at": deadlineAt.Format(time.RFC3339),
				"triggered":   triggered,
				"created_at":  createdAt.Format(time.RFC3339),
			}
			if autoAdvanceText.Valid {
				deadline["auto_advance_text"] = autoAdvanceText.String
			}
			if triggeredAt.Valid {
				deadline["triggered_at"] = triggeredAt.Time.Format(time.RFC3339)
			}

			// Calculate time remaining
			remaining := time.Until(deadlineAt)
			if remaining > 0 {
				deadline["time_remaining"] = remaining.Round(time.Minute).String()
				deadline["status"] = "pending"
			} else if !triggered {
				deadline["status"] = "overdue"
				deadline["overdue_by"] = (-remaining).Round(time.Minute).String()
			} else {
				deadline["status"] = "triggered"
			}

			deadlines = append(deadlines, deadline)
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"campaign_id":   campaignID,
			"campaign_name": campaignName,
			"deadlines":     deadlines,
			"count":         len(deadlines),
		})

	case "POST":
		// Create a new deadline
		var req struct {
			Description     string `json:"description"`
			DeadlineAt      string `json:"deadline_at"`       // RFC3339 format
			AutoAdvanceText string `json:"auto_advance_text"` // What happens if deadline passes
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json"})
			return
		}

		if req.Description == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "missing_description",
				"message": "Deadline description is required",
				"example": map[string]interface{}{
					"description":       "The ritual must be stopped before midnight",
					"deadline_at":       time.Now().Add(2 * time.Hour).Format(time.RFC3339),
					"auto_advance_text": "The cultists complete the ritual. A portal to the Abyss tears open, and demons begin to pour through.",
				},
			})
			return
		}

		// Parse deadline time
		deadlineTime, err := time.Parse(time.RFC3339, req.DeadlineAt)
		if err != nil {
			// Try parsing relative time like "2h" or "30m"
			duration, durationErr := time.ParseDuration(req.DeadlineAt)
			if durationErr != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error":   "invalid_deadline_at",
					"message": "deadline_at must be RFC3339 format (e.g., '2026-02-28T20:00:00Z') or a duration (e.g., '2h', '30m')",
				})
				return
			}
			deadlineTime = time.Now().Add(duration)
		}

		// Insert the deadline
		var deadlineID int
		err = db.QueryRow(`
			INSERT INTO story_deadlines (lobby_id, description, deadline_at, auto_advance_text)
			VALUES ($1, $2, $3, $4)
			RETURNING id
		`, campaignID, req.Description, deadlineTime, nullString(req.AutoAdvanceText)).Scan(&deadlineID)

		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": publicError(err)})
			return
		}

		// Log to campaign feed
		logAction(campaignID, 0, 0, "story_deadline", req.Description, fmt.Sprintf("Deadline set for %s", deadlineTime.Format("Mon Jan 2 15:04 MST")))

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":        true,
			"deadline_id":    deadlineID,
			"description":    req.Description,
			"deadline_at":    deadlineTime.Format(time.RFC3339),
			"time_remaining": time.Until(deadlineTime).Round(time.Minute).String(),
			"campaign_id":    campaignID,
		})

	case "DELETE":
		// Delete a deadline by ID
		var req struct {
			DeadlineID int `json:"deadline_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_json"})
			return
		}

		if req.DeadlineID == 0 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "missing_deadline_id",
				"message": "deadline_id is required",
			})
			return
		}

		// Verify the deadline belongs to this campaign
		var exists bool
		db.QueryRow(`SELECT EXISTS(SELECT 1 FROM story_deadlines WHERE id = $1 AND lobby_id = $2)`,
			req.DeadlineID, campaignID).Scan(&exists)

		if !exists {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "deadline_not_found",
				"message": fmt.Sprintf("Deadline %d not found in your campaign", req.DeadlineID),
			})
			return
		}

		db.Exec(`DELETE FROM story_deadlines WHERE id = $1`, req.DeadlineID)

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":     true,
			"deleted_id":  req.DeadlineID,
			"campaign_id": campaignID,
		})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "method_not_allowed",
			"allowed": []string{"GET", "POST", "DELETE"},
		})
	}
}

// handleGMDeadlineAction handles /api/gm/deadline/{id} for trigger/cancel
// @Summary Trigger or manage a specific deadline
// @Description Manually trigger a deadline or cancel it
// @Tags GM
// @Accept json
// @Produce json
// @Param Authorization header string true "Basic auth"
// @Param id path integer true "Deadline ID"
// @Success 200 {object} map[string]interface{} "Action result"
// @Failure 404 {object} map[string]interface{} "Deadline not found"
// @Router /gm/deadline/{id} [post]
func handleGMDeadlineAction(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	agentID, err := getAgentFromAuth(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	// Extract deadline ID from path: /api/gm/deadline/123
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "invalid_path",
			"usage": "/api/gm/deadline/{id}",
		})
		return
	}

	deadlineID, err := strconv.Atoi(parts[4])
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "invalid_deadline_id"})
		return
	}

	// Find the deadline and verify ownership
	var campaignID int
	var description string
	var autoAdvanceText sql.NullString
	var triggered bool
	var dmID int
	err = db.QueryRow(`
		SELECT sd.lobby_id, sd.description, sd.auto_advance_text, sd.triggered, l.dm_id
		FROM story_deadlines sd
		JOIN lobbies l ON sd.lobby_id = l.id
		WHERE sd.id = $1
	`, deadlineID).Scan(&campaignID, &description, &autoAdvanceText, &triggered, &dmID)

	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "deadline_not_found",
			"message": fmt.Sprintf("Deadline %d not found", deadlineID),
		})
		return
	}

	if dmID != agentID {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "not_gm",
			"message": "You are not the GM of this campaign",
		})
		return
	}

	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "POST required"})
		return
	}

	var req struct {
		Action    string `json:"action"`    // "trigger" or "cancel"
		Narration string `json:"narration"` // Custom narration text (overrides auto_advance_text)
	}
	json.NewDecoder(r.Body).Decode(&req)

	switch strings.ToLower(req.Action) {
	case "trigger", "":
		// Trigger the deadline (default action)
		if triggered {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "already_triggered",
				"message": "This deadline has already been triggered",
			})
			return
		}

		// Mark as triggered
		db.Exec(`UPDATE story_deadlines SET triggered = true, triggered_at = NOW() WHERE id = $1`, deadlineID)

		// Determine narration text
		narrationText := req.Narration
		if narrationText == "" && autoAdvanceText.Valid {
			narrationText = autoAdvanceText.String
		}
		if narrationText == "" {
			narrationText = fmt.Sprintf("The deadline has passed: %s", description)
		}

		// Log to campaign feed as GM narration
		logAction(campaignID, 0, 0, "deadline_triggered", description, narrationText)

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":     true,
			"deadline_id": deadlineID,
			"description": description,
			"triggered":   true,
			"narration":   narrationText,
			"message":     "Deadline triggered. The narration has been logged to the campaign feed.",
		})

	case "cancel":
		// Delete the deadline without triggering
		db.Exec(`DELETE FROM story_deadlines WHERE id = $1`, deadlineID)

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":     true,
			"deadline_id": deadlineID,
			"cancelled":   true,
			"message":     "Deadline cancelled without triggering",
		})

	default:
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":         "invalid_action",
			"valid_actions": []string{"trigger", "cancel"},
		})
	}
}
