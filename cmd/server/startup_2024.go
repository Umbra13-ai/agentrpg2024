package main

// Starting the server: the database comes first (audit AUD-13).
//
// Before Nov 16 2026 a start without the database still served: a failed ping was only logged, the schema, the SRD data and the
// background workers were skipped and never retried, /health answered "ok" and every spell was unknown until someone restarted
// the server by hand. Now:
//   - without DATABASE_URL the server refuses to start;
//   - each step (reaching Postgres, the schema, the SRD data) is tried startupAttempts times, waiting longer each time, with a
//     log line for each failure; if a step still fails the server exits with that error, so the platform shows a failed start
//     and keeps the previous version serving;
//   - the server opens its port only after every step succeeded, and /health asks the database each time (web_pages.go).

import (
	"errors"
	"fmt"
	"log"
	"time"
)

const (
	startupAttempts  = 5
	startupFirstWait = 2 * time.Second // then 4, 8 and 16 seconds: about half a minute in all
	healthTimeout    = 2 * time.Second // /health's ping
)

// startupWait is how long to wait before the given retry (a variable so the tests don't wait).
var startupWait = func(retry int) time.Duration { return startupFirstWait << (retry - 1) }

// startDatabase opens the database, makes the schema and loads the SRD data, or says why it couldn't.
func startDatabase(dbURL string) error {
	if dbURL == "" {
		return errors.New("DATABASE_URL is not set: the server keeps everything in Postgres and does not run without it (BUILD.md)")
	}
	var err error
	if db, err = openDB(dbURL); err != nil { // lib/pq with each failure told to its request, the session settings and the pool limits (db_guard_2024.go)
		return fmt.Errorf("opening the database: %w", err)
	}
	if err := retryStartup("reaching the database", db.Ping); err != nil {
		return err
	}
	log.Println("Connected to Postgres")
	if err := retryStartup("the schema", migrate); err != nil { // database_init.go
		return err
	}
	log.Printf("Database schema ready (%d steps)", len(schemaSteps))
	return retryStartup("the SRD data", seedAndLoad)
}

// retryStartup runs one startup step until it succeeds, startupAttempts times at most.
func retryStartup(what string, step func() error) error {
	var err error
	for attempt := 1; attempt <= startupAttempts; attempt++ {
		if err = step(); err == nil {
			if attempt > 1 {
				log.Printf("Startup: %s succeeded on attempt %d of %d", what, attempt, startupAttempts)
			}
			return nil
		}
		if attempt < startupAttempts {
			wait := startupWait(attempt)
			log.Printf("Startup: %s failed (attempt %d of %d): %v. Trying again in %s.", what, attempt, startupAttempts, err, wait)
			time.Sleep(wait)
		}
	}
	return fmt.Errorf("%s failed %d times, the last time with: %w", what, startupAttempts, err)
}

// seedAndLoad writes the built-in SRD 5.2.1 data into the database and reads it back into memory. Each seed is safe to run again.
func seedAndLoad() error {
	seedCampaignTemplates()
	seedClasses2024()     // the 12 classes of the SRD 5.2.1
	ensureAvatarOfDeath() // the Mysterious Deck's Skull card and the Figurine's Ebony Fly as monsters the GM can add (F207)
	ensureGiantFly()
	syncArmorToSRDTable()
	loadArmorInfoCache()    // the armor table, read once (armor_and_weight.go)
	applyLocalBackgrounds() // a private server's background narratives (local_content.go)
	applyLocalFeats()       // a private server's feats beyond the SRD, such as War Caster (hands_2024.go)
	seedSpells2024()
	seedMonsters2024()
	if err := loadSRDFromDB(); err != nil {
		return err
	}
	seedItemCatalog()
	backfillPreparedLists() // 2024: every caster has a prepared list (prepared_spells_2024.go)
	return nil
}
