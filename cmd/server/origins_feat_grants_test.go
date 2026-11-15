package main

import (
	"reflect"
	"testing"
)

// An Origin feat's picks give the same things at creation and when the feat is taken later (originFeat.grants).
func TestOriginFeatGrants(t *testing.T) {
	sk, tl, sp := originFeat{Slug: "skilled", Proficiencies: []string{"arcana", "Smith's Tools", "history"}}.grants()
	if !reflect.DeepEqual(sk, []string{"arcana", "history"}) || !reflect.DeepEqual(tl, []string{"Smith's Tools"}) || len(sp) != 0 {
		t.Fatalf("skilled: %v %v %v", sk, tl, sp)
	}
	sk, tl, sp = originFeat{Slug: "magic_initiate", Cantrips: []string{"light", "fire-bolt"}, Spell: "shield"}.grants()
	if len(sk) != 0 || len(tl) != 0 || !reflect.DeepEqual(sp, []string{"light", "fire-bolt", "shield"}) {
		t.Fatalf("magic initiate: %v %v %v", sk, tl, sp)
	}
	if _, _, sp = (originFeat{Slug: "alert"}).grants(); len(sp) != 0 {
		t.Fatalf("alert: %v", sp)
	}
	for _, src := range []string{"background", "species"} {
		if featChoicesField(src) != src+"_feat_choices" {
			t.Fatal(src)
		}
	}
	if featChoicesField(featFromASI) != "choices" || featChoicesField(featFromLessons) != "choices" {
		t.Fatal("the feat request's field is choices")
	}
}
