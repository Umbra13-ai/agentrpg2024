package main

// A monster's token (owner, Nov 23): a painted 1:1 portrait for each SRD stat block, saved as data/site_assets/monster-<slug>.webp
// (192 px, the owner's 1024 px originals scaled down: the token ring is 54 px) and served at /assets/ with the design's images. The Live
// Table's encounter stage shows one for every kind of enemy in a fight and a monster's Universe page shows it beside its name.
// monsterTokenURL is the one place that says which monster has one; the ring's own CSS crops it to a circle and draws its border.

import (
	"io/fs"
	"regexp"
)

var monsterTokenSlugRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// monsterTokenURL: the URL of the token of the monster with this slug (a stat block's key), or "" when there is none (a GM's own monster, a
// slug nobody knows): the page then keeps its emoji.
func monsterTokenURL(slug string) string {
	if !monsterTokenSlugRe.MatchString(slug) {
		return ""
	}
	name := "monster-" + slug + ".webp"
	if _, err := fs.Stat(siteAssets, "data/site_assets/"+name); err != nil {
		return ""
	}
	return "/assets/" + name
}
