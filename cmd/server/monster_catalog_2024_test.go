package main

import (
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

// The SRD monster catalogue is the 330 stat blocks (srdMonsterCatalog): the creatures an item gives (the Figurine's Giant Fly, the
// Deck's Avatar of Death) live in the same table but are not counted, listed or offered as a beast form (the Universe page said 332).
// The Universe page's monster panel shows the Challenge Rating groups with their counts, each opening the list with that filter.
func TestPostgresMonsterCatalogIsTheSRDStatBlocks(t *testing.T) {
	testPostgres(t)
	ensureGiantFly()
	ensureAvatarOfDeath()
	want := len(srdMonsters2024())
	if want != 330 {
		t.Fatalf("the SRD file holds %d stat blocks", want)
	}
	var all, catalog int
	db.QueryRow(`SELECT COUNT(*) FROM monsters`).Scan(&all)
	db.QueryRow(`SELECT COUNT(*) FROM monsters WHERE ` + srdMonsterCatalog).Scan(&catalog)
	if catalog != want || all != want+2 {
		t.Errorf("catalogue %d of %d rows, want %d of %d", catalog, all, want, want+2)
	}
	get := func(path string) string {
		r := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		newServer("").Handler.ServeHTTP(w, r)
		b, _ := io.ReadAll(w.Body)
		return string(b)
	}
	if tab := get("/universe"); !strings.Contains(tab, fmt.Sprintf(`"count":"%d creatures"`, want)) {
		t.Errorf("the Universe tab doesn't count %d creatures", want)
	}
	page := get("/universe/srd") // the compendium's browser
	if !strings.Contains(page, fmt.Sprintf("%d creatures", want)) || !strings.Contains(page, "/universe/monsters?cr=CR+0+to+1") {
		t.Errorf("the compendium doesn't count %d creatures or link the Challenge Rating groups", want)
	}
	sum := 0
	for _, b := range crBands {
		var n int
		fmt.Sscanf(page[strings.Index(page, b.Label+" (")+len(b.Label)+2:], "%d", &n)
		sum += n
	}
	if sum != want {
		t.Errorf("the Challenge Rating groups add up to %d, want %d", sum, want)
	}
	list := get("/universe/monsters")
	if strings.Contains(list, "Giant Fly") || strings.Contains(list, "Avatar of Death") || !strings.Contains(list, `data-key="cr"`) {
		t.Errorf("the monster list shows an item's creature, or has no Challenge filter")
	}
	if api := get("/api/universe/monsters"); strings.Contains(api, "giant-fly") || strings.Contains(api, "avatar-of-death") {
		t.Errorf("GET /api/universe/monsters lists an item's creature")
	}
	var flyBeast int
	db.QueryRow(`SELECT COUNT(*) FROM monsters WHERE LOWER(type) = 'beast' AND ` + srdMonsterCatalog + ` AND slug = 'giant-fly'`).Scan(&flyBeast)
	if flyBeast != 0 {
		t.Errorf("the Giant Fly is in the beast lists (Wild Shape, pets, beast spells)")
	}
	// the Figurine's Giant Fly has the SRD stat block's Proficiency Bonus (+2) and the figurine's 12 hours (it read 0 and an invented three uses)
	var pb int
	var desc string
	db.QueryRow(`SELECT COALESCE(proficiency_bonus,0), COALESCE(description,'') FROM monsters WHERE slug = 'giant-fly'`).Scan(&pb, &desc)
	if pb != 2 || !strings.Contains(desc, "12 hours") || strings.Contains(desc, "three times") {
		t.Errorf("the Giant Fly: Proficiency Bonus %d, description %q", pb, desc)
	}
	// the item's own creature is still there for its item
	var name string
	if db.QueryRow(`SELECT name FROM monsters WHERE slug = 'giant-fly'`).Scan(&name); name != "Giant Fly" {
		t.Errorf("the Figurine's Giant Fly is gone: %q", name)
	}
}
