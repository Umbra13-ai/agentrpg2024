-- The baseline of how agents use the server (plan v2.1, S0 item 6). READ ONLY: every statement is a SELECT.
-- Run it against the live database: now, after each deploy, and weekly (psql "$DATABASE_URL" -f tools/baseline_2024.sql).
-- api_logs and request_errors are kept 30 days (logKeepDays), so the first run must come before the logs age out; an account's
-- deletion removes its api_logs rows, and request_errors keeps no address. What the logs cannot show is listed at the end; it is a gap, not a zero.
--
-- Each run records its own header (the time, the window, the build you pass in), so two runs can be compared.
-- Pass the build: psql -v build="'3ac2b31'" -f tools/baseline_2024.sql
-- Play is told from test and operator traffic by the route: /api/admin/* and /api/mod/* are the operator's; a campaign's name decides
-- nothing here (the logs hold ids), so run once against the live database and once against staging.

\set ON_ERROR_STOP on
\pset format aligned
\pset null '(none)'

\echo '== header'
SELECT NOW() AT TIME ZONE 'UTC' AS run_at_utc,
       (SELECT MIN(created_at) FROM api_logs) AS api_logs_from,
       (SELECT COUNT(*) FROM api_logs) AS api_log_rows,
       (SELECT MIN(at) FROM request_errors) AS request_errors_from,
       (SELECT COUNT(*) FROM request_errors) AS request_error_rows,
       :build AS build;

-- 1. Refusals per 100 writes, by error code, players and GMs apart. A write is a POST, PUT, PATCH or DELETE; a GM route starts /api/gm/.
\echo '== 1. refusals per 100 writes'
WITH w AS (
  SELECT CASE WHEN endpoint LIKE '/api/gm/%' THEN 'gm' ELSE 'player' END AS who, response_status
  FROM api_logs
  WHERE method IN ('POST','PUT','PATCH','DELETE') AND endpoint NOT LIKE '/api/admin/%' AND endpoint NOT LIKE '/api/mod/%'
)
SELECT who, COUNT(*) AS writes, COUNT(*) FILTER (WHERE response_status >= 400) AS refused,
       ROUND(100.0 * COUNT(*) FILTER (WHERE response_status >= 400) / NULLIF(COUNT(*), 0), 1) AS refused_per_100
FROM w GROUP BY who ORDER BY who;

\echo '== 1b. the refusals by status and route (the 20 most common)'
SELECT CASE WHEN route LIKE '/api/gm/%' THEN 'gm' ELSE 'player' END AS who, status, error, route, COUNT(*) AS n
FROM request_errors
WHERE route NOT LIKE '/api/admin/%' AND route NOT LIKE '/api/mod/%'
GROUP BY 1, 2, 3, 4 ORDER BY n DESC LIMIT 20;

-- 2. A guessed route: 404 and 405 replies per agent per day.
\echo '== 2. 404 and 405 per agent per day'
SELECT agent_id, date_trunc('day', at)::date AS day, COUNT(*) FILTER (WHERE status = 404) AS not_found, COUNT(*) FILTER (WHERE status = 405) AS wrong_method
FROM request_errors WHERE status IN (404, 405)
GROUP BY 1, 2 ORDER BY COUNT(*) DESC LIMIT 30;

-- 3. Recovery proxy: a refusal followed by a success on the same route within the agent's next three requests.
-- This is a proxy, not proof that the task was finished.
\echo '== 3. a refusal followed by a success on the same route within three requests'
WITH seq AS (
  SELECT id, agent_id, endpoint, method, response_status,
         ROW_NUMBER() OVER (PARTITION BY agent_id ORDER BY id) AS rn
  FROM api_logs WHERE agent_id IS NOT NULL AND endpoint NOT LIKE '/api/admin/%' AND endpoint NOT LIKE '/api/mod/%'
), refused AS (SELECT * FROM seq WHERE response_status >= 400 AND method <> 'GET')
SELECT COUNT(*) AS refusals,
       COUNT(*) FILTER (WHERE EXISTS (SELECT 1 FROM seq s WHERE s.agent_id = r.agent_id AND s.rn BETWEEN r.rn + 1 AND r.rn + 3
                                       AND s.endpoint = r.endpoint AND s.method = r.method AND s.response_status < 400)) AS followed_by_success_same_route
FROM refused r;

-- 4. Time from joining to the first accepted game action, per character.
\echo '== 4. join to first accepted action'
WITH j AS (
  SELECT character_id, MIN(created_at) AS joined FROM api_logs
  WHERE endpoint ~ '^/api/campaigns/[0-9]+/join$' AND response_status < 400 AND character_id IS NOT NULL GROUP BY 1
), a AS (
  SELECT l.character_id, MIN(l.created_at) AS first_action FROM api_logs l JOIN j USING (character_id)
  WHERE l.endpoint IN ('/api/action', '/api/pending-turn') AND l.response_status < 400 AND l.created_at > j.joined GROUP BY 1
)
SELECT COUNT(*) AS characters,
       PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM (a.first_action - j.joined)) / 60) AS median_minutes,
       PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM (a.first_action - j.joined)) / 60) AS p95_minutes
FROM j JOIN a USING (character_id);

-- 5. The size of my-turn and the heartbeat (the stored reply body, where it was kept).
\echo '== 5. reply sizes (bytes)'
SELECT endpoint, COUNT(*) AS reads,
       PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY LENGTH(response_body::text)) AS median_bytes,
       PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY LENGTH(response_body::text)) AS p95_bytes
FROM api_logs WHERE endpoint IN ('/api/my-turn', '/api/heartbeat') AND response_body IS NOT NULL
GROUP BY endpoint ORDER BY endpoint;

-- 6. The share of declares outside a fight. Recorded, never a target: play in the player's own words is intended.
\echo '== 6. declares among game actions'
SELECT COUNT(*) FILTER (WHERE request_body ~ '"action"\s*:\s*"(declare|other|free)"') AS declares,
       COUNT(*) FILTER (WHERE request_body ~ '"action"\s*:') AS actions,
       ROUND(100.0 * COUNT(*) FILTER (WHERE request_body ~ '"action"\s*:\s*"(declare|other|free)"') / NULLIF(COUNT(*) FILTER (WHERE request_body ~ '"action"\s*:'), 0), 1) AS declare_share_pct
FROM api_logs WHERE endpoint = '/api/action' AND method = 'POST' AND response_status < 400;

\echo '== 6b. the action words, most used first'
SELECT substring(request_body from '"action"\s*:\s*"([^"]+)"') AS action_word, COUNT(*) AS n
FROM api_logs WHERE endpoint = '/api/action' AND method = 'POST' AND response_status < 400 AND request_body ~ '"action"\s*:'
GROUP BY 1 ORDER BY n DESC LIMIT 25;

-- 7. Which requests agents read my-turn's lists through (is a short view being used): filled in once S3 ships.
\echo '== 7. my-turn views'
SELECT COALESCE(query_params, '') AS query, COUNT(*) AS n FROM api_logs WHERE endpoint = '/api/my-turn' GROUP BY 1 ORDER BY n DESC LIMIT 10;

-- What these queries cannot show (record it as a gap, never as zero):
--   * a request that never reached the server (a timeout before HTTP, I-ALDRIC-6) is in no table;
--   * a deleted account's rows are gone; rows older than 30 days are pruned; the data starts at the 16 Nov wipe;
--   * request_errors holds no address and no body; response_body is kept only for the routes that store it;
--   * a refusal followed by a success may be a different task; the share of declares says how agents write, not whether they were confused;
--   * a test or operator request is told from play by the route only, so run a separate pass against staging.
