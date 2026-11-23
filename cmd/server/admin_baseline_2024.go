package main

// The baseline of how agents use the server (plan v2.1, S0; tools/baseline_2024.sql is the same set as a file): read only, shown on the admin
// page's Settings tab so it can be read on a phone, with the time and the build it was taken at. What the logs cannot show is said with it.

import (
	"fmt"
	"time"
)

func usageBaseline() map[string]interface{} {
	one := func(q string, dest ...interface{}) { db.QueryRow(q).Scan(dest...) }
	out := map[string]interface{}{"taken_at": time.Now().UTC().Format(time.RFC3339), "build": buildTime, "version": version}
	var from time.Time
	var rows int
	one(`SELECT COALESCE(MIN(created_at), NOW()), COUNT(*) FROM api_logs`, &from, &rows)
	out["api_logs_from"], out["api_log_rows"] = from.UTC().Format(time.RFC3339), rows
	per := func(gm bool) map[string]interface{} {
		cond := `endpoint NOT LIKE '/api/gm/%'`
		if gm {
			cond = `endpoint LIKE '/api/gm/%'`
		}
		var w, f int
		one(`SELECT COUNT(*), COUNT(*) FILTER (WHERE response_status >= 400) FROM api_logs WHERE method IN ('POST','PUT','PATCH','DELETE') AND `+cond+
			` AND endpoint NOT LIKE '/api/admin/%' AND endpoint NOT LIKE '/api/mod/%'`, &w, &f)
		p := 0.0
		if w > 0 {
			p = float64(f) * 100 / float64(w)
		}
		return map[string]interface{}{"writes": w, "refused": f, "refused_per_100": fmt.Sprintf("%.1f", p)}
	}
	out["refusals_players"], out["refusals_gms"] = per(false), per(true)
	var top []map[string]interface{}
	if rs, err := db.Query(`SELECT status, error, regexp_replace(route, '/[0-9]+', '/{id}', 'g'), COUNT(*) FROM request_errors
		WHERE route NOT LIKE '/api/admin/%' AND route NOT LIKE '/api/mod/%' GROUP BY 1, 2, 3 ORDER BY 4 DESC LIMIT 10`); err == nil {
		defer rs.Close()
		for rs.Next() {
			var st, n int
			var e, route string
			rs.Scan(&st, &e, &route, &n)
			top = append(top, map[string]interface{}{"status": st, "error": e, "route": route, "count": n})
		}
	}
	out["top_refusals"] = top
	var nf, wm, days int
	one(`SELECT COUNT(*) FILTER (WHERE status = 404), COUNT(*) FILTER (WHERE status = 405), COUNT(DISTINCT at::date) FROM request_errors WHERE status IN (404, 405)`, &nf, &wm, &days)
	out["guessed_routes"] = map[string]interface{}{"not_found": nf, "wrong_method": wm, "days": days}
	var jc int
	var med, p95 *float64
	one(`WITH j AS (SELECT character_id, MIN(created_at) AS joined FROM api_logs WHERE endpoint ~ '^/api/campaigns/[0-9]+/join$' AND response_status < 400 AND character_id IS NOT NULL GROUP BY 1),
		a AS (SELECT l.character_id, MIN(l.created_at) AS first FROM api_logs l JOIN j USING (character_id) WHERE l.endpoint IN ('/api/action','/api/pending-turn') AND l.response_status < 400 AND l.created_at > j.joined GROUP BY 1)
		SELECT COUNT(*), PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM (a.first - j.joined))/60), PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM (a.first - j.joined))/60) FROM j JOIN a USING (character_id)`, &jc, &med, &p95)
	out["join_to_first_action_minutes"] = map[string]interface{}{"characters": jc, "median": med, "p95": p95}
	sizes := map[string]interface{}{}
	for _, ep := range []string{"/api/my-turn", "/api/heartbeat"} {
		var n int
		var m, p *float64
		one(`SELECT COUNT(*), PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY LENGTH(response_body::text)), PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY LENGTH(response_body::text)) FROM api_logs WHERE endpoint = '`+ep+`' AND response_body IS NOT NULL`, &n, &m, &p)
		sizes[ep] = map[string]interface{}{"reads": n, "median_bytes": m, "p95_bytes": p}
	}
	out["reply_sizes"] = sizes
	var decl, acts int
	one(`SELECT COUNT(*) FILTER (WHERE request_body ~ '"action"\s*:\s*"(declare|other|free|wait)"'), COUNT(*) FILTER (WHERE request_body ~ '"action"\s*:') FROM api_logs WHERE endpoint = '/api/action' AND method = 'POST' AND response_status < 400`, &decl, &acts)
	share := 0.0
	if acts > 0 {
		share = float64(decl) * 100 / float64(acts)
	}
	out["declares"] = map[string]interface{}{"declares": decl, "actions": acts, "share_pct": fmt.Sprintf("%.1f", share)}
	out["gaps"] = "Requests that never reached the server are in no log; rows older than 30 days are pruned and a deleted account's rows are gone; request_errors holds no address or body; test and operator traffic is told from play by the route only; a refusal followed by a success is not measured here (tools/baseline_2024.sql has it)."
	return out
}
