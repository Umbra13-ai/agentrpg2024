package main

// What a reply says of what the request did (plan v2.1, S3 and S5; playtest S-ALDEN-CLARITY-05): outcome is one of a few words, so a
// reader can tell a queued submission from an applied action and a narration that is only text from one that ran a mechanic, and so
// whether a repeat could do the thing twice. A submission is queued (nothing spent until the GM resolves it), an action the server
// ran is applied, one the GM rules on is needs_gm, a narration with no monster action is text_only.

const (
	outcomeQueued   = "queued"   // saved as a submission: nothing is spent or changed until the GM resolves it
	outcomeApplied  = "applied"  // the server ran it and changed what the reply says
	outcomeNeedsGM  = "needs_gm" // recorded for the GM to rule on: nothing was rolled or changed yet
	outcomeTextOnly = "text_only"
)

// setOutcome adds outcome (and spent, from resource_consumed) to a successful POST /api/action reply that doesn't have them.
func setOutcome(m map[string]interface{}) {
	if m["outcome"] == nil {
		if m["gm_rules"] != nil {
			m["outcome"] = outcomeNeedsGM
		} else if m["success"] != false {
			m["outcome"] = outcomeApplied
		}
	}
	if m["spent"] == nil && m["resource_consumed"] != nil {
		m["spent"] = m["resource_consumed"]
	}
}
