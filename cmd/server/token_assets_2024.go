package main

// A monster's token (owner, Nov 23): a painted 1:1 portrait for each SRD stat block, saved as data/site_assets/monster-<slug>.webp
// (192 px, the owner's 1024 px originals scaled down: the token ring is 54 px) and served at /assets/ with the design's images. The Live
// Table's encounter stage shows one for every kind of enemy in a fight and a monster's Universe page shows it beside its name.
// monsterTokenURL is the one place that says which monster has one; the ring's own CSS crops it to a circle and draws its border.

import (
	"io/fs"
	"regexp"
	"strings"
)

var monsterTokenSlugRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// monsterTokenURL: the URL of the token of the monster with this key, or "" when there is none (the page then keeps its emoji): an SRD
// stat block's painted token, or the token a GM uploaded for a compendium monster (a custom: key, monster_token_2024.go).
func monsterTokenURL(slug string) string {
	if strings.HasPrefix(slug, "custom:") {
		return customTokenURL(slug)
	}
	if !monsterTokenSlugRe.MatchString(slug) {
		return ""
	}
	name := "monster-" + slug + ".webp"
	if _, err := fs.Stat(siteAssets, "data/site_assets/"+name); err != nil {
		return ""
	}
	return "/assets/" + name
}
