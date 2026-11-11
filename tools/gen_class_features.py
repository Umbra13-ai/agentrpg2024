"""Generate game/class_features_2024.go from cmd/server/data/srd521_classes.json.

Every class feature (and the SRD subclass's features) becomes a ClassFeature / SubclassFeature whose Description is the
SRD text word for word, and each subclass gets its SRD name, introduction and always-prepared spells (its Spells table).
The mechanics keys the server looks for come from MECH below.

Usage: python tools/gen_class_features.py
Every class in ENABLED is generated (all twelve).
"""
import json
import re
import sys

ROOT = r"<workspace>\agentrpg-src"
ENABLED = ["barbarian", "fighter", "rogue", "paladin", "monk", "ranger", "cleric", "druid", "bard", "wizard", "sorcerer", "warlock"]

# Mechanics keys for the 2024 features: (class or subclass, feature name) -> {key: value}
MECH = {
    ("barbarian", "Rage"): {"rage": "true", "rage_2024": "true"},
    ("barbarian", "Weapon Mastery"): {"weapon_mastery": "true"},
    ("barbarian", "Unarmored Defense"): {"unarmored_defense": "con"},
    ("barbarian", "Danger Sense"): {"danger_sense": "true"},
    ("barbarian", "Reckless Attack"): {"reckless_attack": "true"},
    ("barbarian", "Primal Knowledge"): {"primal_knowledge": "true"},
    ("barbarian", "Extra Attack"): {"extra_attack": "1"},
    ("barbarian", "Fast Movement"): {"fast_movement": "10"},
    ("barbarian", "Feral Instinct"): {"feral_instinct": "true"},
    ("barbarian", "Instinctive Pounce"): {"instinctive_pounce": "true"},
    ("barbarian", "Brutal Strike"): {"brutal_strike": "1d10"},
    ("barbarian", "Relentless Rage"): {"relentless_rage": "true"},
    ("barbarian", "Improved Brutal Strike"): {"improved_brutal_strike": "true"},
    ("barbarian", "Persistent Rage"): {"persistent_rage": "true"},
    ("barbarian", "Indomitable Might"): {"indomitable_might": "true"},
    ("barbarian", "Primal Champion"): {"primal_champion": "true"},
    ("fighter", "Fighting Style"): {"fighting_style": "true"},
    ("fighter", "Second Wind"): {"second_wind": "true"},
    ("fighter", "Weapon Mastery"): {"weapon_mastery": "true"},
    ("fighter", "Action Surge"): {"action_surge": "1"},
    ("fighter", "Tactical Mind"): {"tactical_mind": "true"},
    ("fighter", "Extra Attack"): {"extra_attack": "1"},
    ("fighter", "Tactical Shift"): {"tactical_shift": "true"},
    ("fighter", "Indomitable"): {"indomitable": "1"},
    ("fighter", "Tactical Master"): {"tactical_master": "true"},
    ("fighter", "Two Extra Attacks"): {"extra_attack": "2"},
    ("fighter", "Studied Attacks"): {"studied_attacks": "true"},
    ("fighter", "Three Extra Attacks"): {"extra_attack": "3"},
    ("champion", "Improved Critical"): {"crit_range": "19"},
    ("champion", "Remarkable Athlete"): {"remarkable_athlete": "true"},
    ("champion", "Additional Fighting Style"): {"extra_fighting_style": "true"},
    ("champion", "Heroic Warrior"): {"heroic_warrior": "true"},
    ("champion", "Superior Critical"): {"crit_range": "18"},
    ("champion", "Survivor"): {"survivor_regen": "true", "defy_death": "true"},
    ("rogue", "Expertise"): {"expertise": "2"},
    ("rogue", "Sneak Attack"): {"sneak_attack": "1d6"},
    ("rogue", "Thieves\u2019 Cant"): {"thieves_cant": "true"},
    ("rogue", "Weapon Mastery"): {"weapon_mastery": "true"},
    ("rogue", "Cunning Action"): {"cunning_action": "true"},
    ("rogue", "Steady Aim"): {"steady_aim": "true"},
    ("rogue", "Cunning Strike"): {"cunning_strike": "true"},
    ("rogue", "Uncanny Dodge"): {"uncanny_dodge": "true"},
    ("rogue", "Evasion"): {"evasion": "true"},
    ("rogue", "Reliable Talent"): {"reliable_talent": "true"},
    ("rogue", "Improved Cunning Strike"): {"improved_cunning_strike": "true"},
    ("rogue", "Devious Strikes"): {"devious_strikes": "true"},
    ("rogue", "Slippery Mind"): {"slippery_mind": "true"},
    ("rogue", "Elusive"): {"elusive": "true"},
    ("rogue", "Stroke of Luck"): {"stroke_of_luck": "true"},
    ("thief", "Fast Hands"): {"fast_hands": "true"},
    ("thief", "Second-Story Work"): {"climb_speed": "full", "jump_bonus": "dex_mod"},
    ("thief", "Supreme Sneak"): {"stealth_attack": "true"},
    ("thief", "Use Magic Device"): {"ignore_magic_item_requirements": "true"},
    ("thief", "Thief\u2019s Reflexes"): {"extra_first_round_turn": "true"},
    ("paladin", "Lay On Hands"): {"lay_on_hands": "true"},
    ("paladin", "Spellcasting"): {"spellcasting": "cha"},
    ("paladin", "Weapon Mastery"): {"weapon_mastery": "true"},
    ("paladin", "Fighting Style"): {"fighting_style": "true"},
    ("paladin", "Paladin\u2019s Smite"): {"paladins_smite": "true"},
    ("paladin", "Channel Divinity"): {"channel_divinity": "true", "divine_sense": "true"},
    ("paladin", "Extra Attack"): {"extra_attack": "1"},
    ("paladin", "Faithful Steed"): {"faithful_steed": "true"},
    ("paladin", "Aura of Protection"): {"aura_of_protection": "10"},
    ("paladin", "Abjure Foes"): {"abjure_foes": "true"},
    ("paladin", "Aura of Courage"): {"aura_of_courage": "true"},
    ("paladin", "Radiant Strikes"): {"radiant_strikes": "true"},
    ("paladin", "Restoring Touch"): {"restoring_touch": "true"},
    ("paladin", "Aura Expansion"): {"aura_of_protection": "30"},
    ("devotion", "Oath of Devotion Spells"): {"oath_spells": "true"},
    ("devotion", "Sacred Weapon"): {"sacred_weapon": "true"},
    ("devotion", "Aura of Devotion"): {"aura_of_devotion": "true"},
    ("devotion", "Smite of Protection"): {"smite_of_protection": "true"},
    ("devotion", "Holy Nimbus"): {"holy_nimbus": "true"},
    ("monk", "Martial Arts"): {"martial_arts": "d6"},
    ("monk", "Unarmored Defense"): {"unarmored_defense": "wis"},
    ("monk", "Monk\u2019s Focus"): {"focus": "true", "focus_points": "true"},
    ("monk", "Unarmored Movement"): {"unarmored_movement": "10"},
    ("monk", "Uncanny Metabolism"): {"uncanny_metabolism": "true"},
    ("monk", "Deflect Attacks"): {"deflect_missiles": "true", "deflect_attacks": "true"},
    ("monk", "Slow Fall"): {"slow_fall": "true"},
    ("monk", "Extra Attack"): {"extra_attack": "1"},
    ("monk", "Stunning Strike"): {"stunning_strike": "true"},
    ("monk", "Empowered Strikes"): {"empowered_strikes": "true"},
    ("monk", "Evasion"): {"evasion": "true"},
    ("monk", "Acrobatic Movement"): {"acrobatic_movement": "true"},
    ("monk", "Heightened Focus"): {"heightened_focus": "true"},
    ("monk", "Self-Restoration"): {"self_restoration": "true"},
    ("monk", "Deflect Energy"): {"deflect_energy": "true"},
    ("monk", "Disciplined Survivor"): {"disciplined_survivor": "true"},
    ("monk", "Perfect Focus"): {"perfect_focus": "true"},
    ("monk", "Superior Defense"): {"superior_defense": "true"},
    ("monk", "Body and Mind"): {"body_and_mind": "true"},
    ("open-hand", "Open Hand Technique"): {"open_hand_technique": "true"},
    ("open-hand", "Wholeness of Body"): {"wholeness_of_body": "true"},
    ("open-hand", "Fleet Step"): {"fleet_step": "true"},
    ("open-hand", "Quivering Palm"): {"quivering_palm": "true"},
    ("ranger", "Spellcasting"): {"spellcasting": "wis"},
    ("ranger", "Favored Enemy"): {"favored_enemy": "true"},
    ("ranger", "Weapon Mastery"): {"weapon_mastery": "true"},
    ("ranger", "Deft Explorer"): {"deft_explorer": "true"},
    ("ranger", "Fighting Style"): {"fighting_style": "true"},
    ("ranger", "Extra Attack"): {"extra_attack": "1"},
    ("ranger", "Roving"): {"roving": "10"},
    ("ranger", "Expertise"): {"expertise": "2"},
    ("ranger", "Tireless"): {"tireless": "true"},
    ("ranger", "Relentless Hunter"): {"relentless_hunter": "true"},
    ("ranger", "Nature\u2019s Veil"): {"natures_veil": "true"},
    ("ranger", "Precise Hunter"): {"precise_hunter": "true"},
    ("ranger", "Feral Senses"): {"feral_senses": "30"},
    ("ranger", "Foe Slayer"): {"foe_slayer": "true"},
    ("hunter", "Hunter\u2019s Lore"): {"hunters_lore": "true"},
    ("hunter", "Hunter\u2019s Prey"): {"hunters_prey": "true"},
    ("hunter", "Defensive Tactics"): {"defensive_tactics": "true"},
    ("hunter", "Superior Hunter\u2019s Prey"): {"superior_hunters_prey": "true"},
    ("hunter", "Superior Hunter\u2019s Defense"): {"superior_hunters_defense": "true"},
    ("cleric", "Spellcasting"): {"spellcasting": "wis"},
    ("cleric", "Divine Order"): {"divine_order": "true"},
    ("cleric", "Channel Divinity"): {"channel_divinity": "true", "divine_spark": "true", "turn_undead": "true"},
    ("cleric", "Sear Undead"): {"sear_undead": "true"},
    ("cleric", "Blessed Strikes"): {"blessed_strikes": "true", "divine_strike": "1d8"},
    ("cleric", "Divine Intervention"): {"divine_intervention": "true"},
    ("cleric", "Improved Blessed Strikes"): {"improved_blessed_strikes": "true"},
    ("cleric", "Greater Divine Intervention"): {"greater_divine_intervention": "true"},
    ("life", "Disciple of Life"): {"disciple_of_life": "true", "bonus_healing": "2+spell_level"},
    ("life", "Life Domain Spells"): {"domain_spells": "true"},
    ("life", "Preserve Life"): {"preserve_life": "true"},
    ("life", "Blessed Healer"): {"blessed_healer": "true"},
    ("life", "Supreme Healing"): {"supreme_healing": "true"},
    ("druid", "Spellcasting"): {"spellcasting": "wis"},
    ("druid", "Druidic"): {"druidic": "true"},
    ("druid", "Primal Order"): {"primal_order": "true"},
    ("druid", "Wild Shape"): {"wild_shape": "true"},
    ("druid", "Wild Companion"): {"wild_companion": "true"},
    ("druid", "Wild Resurgence"): {"wild_resurgence": "true"},
    ("druid", "Elemental Fury"): {"elemental_fury": "true", "primal_strike": "1d8"},
    ("druid", "Improved Elemental Fury"): {"improved_elemental_fury": "true"},
    ("druid", "Beast Spells"): {"beast_spells": "true"},
    ("druid", "Archdruid"): {"archdruid": "true"},
    ("land", "Circle of the Land Spells"): {"circle_spells": "true"},
    ("land", "Land\u2019s Aid"): {"lands_aid": "true"},
    ("land", "Natural Recovery"): {"natural_recovery": "true"},
    ("land", "Nature\u2019s Ward"): {"natures_ward": "true"},
    ("land", "Nature\u2019s Sanctuary"): {"natures_sanctuary": "true"},
    ("bard", "Bardic Inspiration"): {"bardic_inspiration": "d6"},
    ("bard", "Spellcasting"): {"spellcasting": "cha"},
    ("bard", "Expertise"): {"expertise": "2"},
    ("bard", "Jack of All Trades"): {"jack_of_all_trades": "true"},
    ("bard", "Font of Inspiration"): {"font_of_inspiration": "true"},
    ("bard", "Countercharm"): {"countercharm": "true"},
    ("bard", "Magical Secrets"): {"magical_secrets": "true"},
    ("bard", "Superior Inspiration"): {"superior_inspiration": "true"},
    ("bard", "Words of Creation"): {"words_of_creation": "true"},
    ("lore", "Bonus Proficiencies"): {"bonus_proficiencies": "3"},
    ("lore", "Cutting Words"): {"cutting_words": "true"},
    ("lore", "Magical Discoveries"): {"magical_discoveries": "true"},
    ("lore", "Peerless Skill"): {"peerless_skill": "true"},
    ("wizard", "Spellcasting"): {"spellcasting": "int"},
    ("wizard", "Ritual Adept"): {"ritual_adept": "true"},
    ("wizard", "Arcane Recovery"): {"arcane_recovery": "true"},
    ("wizard", "Scholar"): {"scholar": "true"},
    ("wizard", "Memorize Spell"): {"memorize_spell": "true"},
    ("wizard", "Spell Mastery"): {"spell_mastery": "true"},
    ("wizard", "Signature Spells"): {"signature_spells": "true"},
    ("evocation", "Evocation Savant"): {"evocation_savant": "true"},
    ("evocation", "Potent Cantrip"): {"potent_cantrip": "true"},
    ("evocation", "Sculpt Spells"): {"sculpt_spells": "true"},
    ("evocation", "Empowered Evocation"): {"empowered_evocation": "true"},
    ("evocation", "Overchannel"): {"overchannel": "true"},
    ("sorcerer", "Spellcasting"): {"spellcasting": "cha"},
    ("sorcerer", "Innate Sorcery"): {"innate_sorcery": "true"},
    ("sorcerer", "Font of Magic"): {"sorcery_points": "true"},
    ("sorcerer", "Metamagic"): {"metamagic": "true"},
    ("sorcerer", "Sorcerous Restoration"): {"sorcerous_restoration": "true"},
    ("sorcerer", "Sorcery Incarnate"): {"sorcery_incarnate": "true"},
    ("sorcerer", "Arcane Apotheosis"): {"arcane_apotheosis": "true"},
    ("draconic", "Draconic Resilience"): {"draconic_resilience": "true", "natural_ac": "13+dex", "bonus_hp_per_level": "1"},
    ("draconic", "Draconic Spells"): {"domain_spells": "true"},
    ("draconic", "Elemental Affinity"): {"elemental_affinity": "true"},
    ("draconic", "Dragon Wings"): {"dragon_wings": "true"},
    ("draconic", "Dragon Companion"): {"dragon_companion": "true"},
    ("warlock", "Eldritch Invocations"): {"eldritch_invocations": "true"},
    ("warlock", "Pact Magic"): {"pact_magic": "cha"},
    ("warlock", "Magical Cunning"): {"magical_cunning": "true"},
    ("warlock", "Contact Patron"): {"contact_patron": "true"},
    ("warlock", "Mystic Arcanum"): {"mystic_arcanum": "true"},
    ("warlock", "Eldritch Master"): {"eldritch_master": "true"},
    ("fiend", "Dark One\u2019s Blessing"): {"dark_ones_blessing": "true"},
    ("fiend", "Fiend Spells"): {"domain_spells": "true"},
    ("fiend", "Dark One\u2019s Own Luck"): {"dark_ones_own_luck": "true"},
    ("fiend", "Fiendish Resilience"): {"fiendish_resilience": "true"},
    ("fiend", "Hurl Through Hell"): {"hurl_through_hell": "true"},
    ("berserker", "Frenzy"): {"frenzy": "true"},
    ("berserker", "Mindless Rage"): {"mindless_rage": "true", "rage_immune_charm": "true", "rage_immune_frightened": "true"},
    ("berserker", "Retaliation"): {"retaliation": "true", "retaliation_attack": "true"},
    ("berserker", "Intimidating Presence"): {"intimidating_presence": "true"},
}


def gostr(s):
    return json.dumps(s, ensure_ascii=False)


def text_of(blocks):
    out = []
    for b in blocks:
        if isinstance(b, str):
            out.append(b)
        elif isinstance(b, dict):
            if "trait" in b:
                out.append(f'{b["trait"]}. {b["text"]}')
            elif "bullet" in b:
                out.append("- " + b["bullet"])
            elif "table" in b:
                t = b["table"]
                out.append((t.get("title") or "") + ": " + " / ".join(" | ".join(map(str, r)) for r in t["rows"]))
            elif "keyvalue" in b:
                out.append("; ".join(f'{r["label"]}: {r["text"]}' for r in b["keyvalue"]))
            elif "sidebar" in b:
                out.append(" ".join(b["sidebar"]))
    return "\n".join(out)


def spell_slug(name):
    return re.sub(r"[^a-z0-9]+", "-", name.lower().replace("\u2019", "").replace("'", "")).strip("-")


def features_of(cls):
    """Return (class features, subclass name, subclass features, subclass introduction, always-prepared spells by level)."""
    cf, sf, sub, intro, spells = [], [], "", "", {}
    mode = None
    for s in cls["sections"]:
        h = s["heading"]
        if s["level"] == 2:
            if h.endswith("Class Features"):
                mode = "class"
            elif h.startswith(cls["name"] + " Subclass:"):
                mode, sub = "sub", h.split(":", 1)[1].strip()
                intro = text_of([b for b in s["blocks"] if not (isinstance(b, dict) and "table" in b)])
                take_spell_table(s["blocks"], sub, spells)  # the extraction can put the table at the top of the section
            else:
                mode = None
            continue
        m = re.match(r"Level (\d+): (.+)", h)
        if not m or s["level"] != 3 or mode is None:
            continue
        item = (int(m.group(1)), m.group(2).strip(), text_of(s["blocks"]))
        (cf if mode == "class" else sf).append(item)
        # the subclass's spells table ("Life Domain Spells": Cleric Level | Prepared Spells); the Circle of the Land has one per land
        # type, handled by game.LandCircleSpells
        if mode == "sub" and m.group(2).strip().endswith("Spells"):
            take_spell_table(s["blocks"], sub, spells)
    return cf, sub, sf, intro, spells


def take_spell_table(blocks, sub, spells):
    """read a subclass spells table (Level | Spells) into spells; the Circle of the Land has one per land type (game.LandCircleSpells)"""
    if sub == "Circle of the Land":
        return
    for b in blocks:
        if isinstance(b, dict) and "table" in b and (b["table"].get("title") or "").endswith("Spells"):
            rows = b["table"]["rows"]
            if rows and len(rows[0]) == 2 and str(rows[0][0]).endswith("Level"):
                for r in rows[1:]:
                    if str(r[0]).strip().isdigit():
                        spells[int(r[0])] = [spell_slug(x) for x in str(r[1]).split(",") if x.strip()]


def main():
    data = json.load(open(ROOT + r"\cmd\server\data\srd521_classes.json", encoding="utf-8"))
    known_spells = {spell_slug(x["name"]) for x in json.load(open(ROOT + r"\cmd\server\data\srd521_spells.json", encoding="utf-8"))}
    out = ["package game", "", "// Code generated by tools/gen_class_features.py from the SRD 5.2.1 text; DO NOT EDIT.", "",
           "func init() {"]
    for cls in data:
        key = cls["name"].lower()
        if key not in ENABLED:
            continue
        cf, sub, sf, intro, spells = features_of(cls)
        out.append(f"\tclassFeatures[{gostr(key)}] = []ClassFeature{{")
        for lvl, name, desc in cf:
            mech = MECH.get((key, name))
            ms = "" if not mech else ", Mechanics: map[string]string{" + ", ".join(f"{gostr(k)}: {gostr(v)}" for k, v in mech.items()) + "}"
            out.append(f"\t\t{{Name: {gostr(name)}, Level: {lvl}, Description: {gostr(desc)}{ms}}},")
        out.append("\t}")
        if sf:
            slug = {"Path of the Berserker": "berserker", "Oath of Devotion": "devotion", "Warrior of the Open Hand": "open-hand", "Life Domain": "life", "Circle of the Land": "land", "College of Lore": "lore", "Evoker": "evocation", "Draconic Sorcery": "draconic", "Fiend Patron": "fiend"}.get(sub, re.sub(r"[^a-z]+", "_", sub.lower()).strip("_"))
            out.append(f"\tif sc, ok := AvailableSubclasses[{gostr(slug)}]; ok {{")
            out.append("		sc.SubclassLevel = 3 // 2024: every subclass is chosen at level 3")
            out.append(f"\t\tsc.Name = {gostr(sub)}")
            out.append(f"\t\tsc.Description = {gostr(intro)}")
            if spells:
                for lvl, lst in spells.items():
                    missing = [x for x in lst if x not in known_spells]
                    assert not missing, (sub, missing)
                parts = ", ".join(f"{lvl}: {{" + ", ".join(gostr(x) for x in lst) + "}" for lvl, lst in sorted(spells.items()))
                out.append(f"\t\tsc.DomainSpells = map[int][]string{{{parts}}} // the subclass's spells table: always prepared")
            out.append("\t\tsc.Features = []SubclassFeature{")
            for lvl, name, desc in sf:
                mech = MECH.get((slug, name))
                ms = "" if not mech else ", Mechanics: map[string]string{" + ", ".join(f"{gostr(k)}: {gostr(v)}" for k, v in mech.items()) + "}"
                out.append(f"\t\t\t{{Name: {gostr(name)}, Level: {lvl}, Description: {gostr(desc)}{ms}}},")
            out.append("\t\t}")
            out.append(f"\t\tAvailableSubclasses[{gostr(slug)}] = sc")
            out.append("\t}")
    out.append("}")
    open(ROOT + r"\game\class_features_2024.go", "w", encoding="utf-8", newline="\n").write("\n".join(out) + "\n")
    print("written")


main()
