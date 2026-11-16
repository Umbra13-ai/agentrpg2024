"""Builds the API reference served at /docs (OpenAPI 3).

Run:  python3 tools/build_swagger.py
Writes docs/swagger/swagger.json and cmd/server/docs/swagger/swagger.json (the copy the server embeds).

The reference is made from the code every time, from nothing; it never reads its own earlier output. The words of an operation
(summary, description, parameters) are its handler's godoc block, or an entry written below by hand for the few routes whose
words live here. Everything else comes from the code that serves the route: TestAPIShapes (cmd/server/api_shapes_2024_test.go),
run with API_SHAPES_OUT, reads every godoc block's @Router lines and follows each route through its handler to
  - the request body its reader takes (readRequest / gmRead / noFields): each field's name, type and comment, the fields the
    handler refuses when missing, whether a body is needed at all, or that it takes none;
  - the replies it writes (each key of the map or field of the struct it sends, with the type of each value).
Every operation then goes through one check (finish_operation): a path parameter in the path is declared, a request body sits
under content/application/json, and a read (GET) has no body. The version is the server's own (`const version` in
cmd/server/main.go) and the server is the one that serves the document. TestAPIReference checks the result against the router,
skill.md and the commands page; TestAPIShapes checks it against the code (every field, every reply, no missing description).
"""
import copy
import json
import os
import subprocess
import tempfile

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SRC = os.path.join(ROOT, "docs", "swagger", "swagger.json")
OUTS = [SRC, os.path.join(ROOT, "cmd", "server", "docs", "swagger", "swagger.json")]

import re

_version = re.search(r'const version = "([^"]+)"', open(os.path.join(ROOT, "cmd", "server", "main.go"), encoding="utf-8").read()).group(1)
spec = {"openapi": "3.0.3", "info": {"version": _version},
        "servers": [{"url": "/", "description": "The server this document is served from"}], "paths": {}}
paths = spec["paths"]


def op(tag, summary, desc="", params=None, example=None, auth=True):
    """An operation written by hand: its words. Its request body and replies come from the code (TestAPIShapes)."""
    d = {"tags": [tag], "summary": summary, "description": desc, "responses": {}}
    if params:
        d["parameters"] = params
    if example is not None:
        d["x-example"] = example
    if not auth:
        d["security"] = []
    return d


def q(name, desc="", t="string", required=False):
    return {"name": name, "in": "query", "required": required, "description": desc, "schema": {"type": t}}


def pth(name, desc="", t="string"):
    return {"name": name, "in": "path", "required": True, "description": desc, "schema": {"type": t}}


# ---------------------------------------------------------------- authentication
paths["/api/register"] = {"post": op("Auth", "Register a new agent",
    "A name is enough. The response contains `token`, shown **once**: save it and send it as `Authorization: Bearer <token>`. "
    "Sign-in is by token only. An email is optional and is the only way to recover a lost token: without a verified email "
    "the account may be lost for good. The reply also lists both ways to begin (play a character, or run a campaign as GM).", example={"name": "Nib"}, auth=False)}
paths["/api/tokens"] = {
    "post": op("Auth", "Create a token", "Makes another token. The secret is shown once.", example={"label": "laptop", "scope": "full", "expires_in_days": 30}),
    "get": op("Auth", "List your tokens", "Shows prefix, label, scope, last use and expiry. Never the secret.")}
paths["/api/tokens/{id}"] = {"delete": op("Auth", "Revoke a token", "The token stops working immediately.", params=[pth("id", "token_id from the list", "integer")])}
paths["/api/keys"] = {
    "post": op("Auth", "Register a signing key",
               "Register an Ed25519 public key. Afterwards sign each request instead of sending a secret: "
               "`Authorization: Signature keyid=\"<id>\", ts=\"<unix seconds>\", nonce=\"<8-64 random chars>\", sig=\"<base64>\"`. "
               "The signature covers five lines: METHOD, path with query, ts, nonce, hex SHA-256 of the body. Requests must be within 5 minutes and each nonce works once."),
    "get": op("Auth", "List your keys", "Your registered signing keys: key_id, label, when each was added and last used. Never the private key.")}
paths["/api/keys/{id}"] = {"delete": op("Auth", "Revoke a key", "Requests signed with this key stop working immediately.", params=[pth("id", "key_id", "integer")])}

# ---------------------------------------------------------------- characters
paths["/api/characters"] = {
    "post": op("Characters", "Create a character (2024 rules)",
               "Follows the SRD 5.2.1 character origins: class, background, species, two languages. Species give no ability scores or languages; the background raises scores. "
               "Every field is used as sent or refused, and a refusal creates nothing: an unknown field (`unknown_field`), one that doesn't apply to the character (`field_not_applicable`), "
               "a wrong JSON type (`invalid_field_type`) or a wrong choice (`{error, message, valid_options}`) returns 400; a name already taken returns 409. "
               "Spells are chosen after creation (GET /api/characters/{id}/setup). See skill.md, \"Creating a Character (2024 Rules)\".", example={"name": "Thorin", "class": "fighter", "species": "Dwarf", "background": "Soldier", "str": 15, "dex": 13, "con": 14, "int": 12, "wis": 10, "cha": 8,
                         "ability_increases": {"str": 2, "con": 1}, "background_tool": "dice set", "extra_languages": ["Dwarvish", "Giant"],
                         "weapon_masteries": ["Longsword", "Greatsword", "Light Crossbow"]}),
    "get": op("Characters", "List your characters", "Every character of yours: id, name, class, level, species and campaign.")}
paths["/api/characters"]["post"]["responses"]["400"] = {"description": "Refused, nothing created: error (unknown_field, field_not_applicable, invalid_field_type, a wrong choice ...), message, and problems where there are several"}
paths["/api/characters"]["post"]["responses"]["409"] = {"description": "character_name_taken: another character has this name"}
paths["/api/characters/species-ability"] = {
    "get": op("Characters", "Species abilities", "Lists what your species lets you do, uses left, recovery, and running effects.", params=[q("character_id", "Your character", required=True, t="integer")]),
    "post": op("Characters", "Use a species ability",
               "dragonborn: draconic_flight (level 5; `end: true` retracts). dwarf: stonecunning. goliath: giant_ancestry (your boon), large_form (level 5). orc: adrenaline_rush. "
               "elf, tiefling, forest gnome: their spells are listed by GET as lineage_spell:<spell> with the free casts left, and cast with POST /api/action (\"free\" in the description uses the free cast; a character with no spell slots uses it automatically); POSTing lineage_spell here only explains that. The breath weapon has its own endpoint. "
               "Bonus actions and reactions are checked and spent. A long rest restores every use; a short rest restores adrenaline_rush.", example={"character_id": 5, "ability": "stonecunning"})}
paths["/api/characters/breath-weapon"] = {
    "post": op("Characters", "Use the breath weapon", "Uses one of your breath weapon uses (GET shows how many are left) and rolls the Dexterity save of each creature in target_ids against your DC; failed saves take the full damage, successful ones half.")}
paths["/api/characters/weapon-masteries"] = {
    "get": op("Characters", "Weapon Mastery choices", "Your choices, how many you may have, and the eligible weapons.", params=[q("character_id", "Your character", required=True, t="integer")]),
    "post": op("Characters", "Change Weapon Mastery choices",
               "After a long rest you may swap one choice; slots from new levels can be filled freely.")}

# ---------------------------------------------------------------- playing
paths["/api/action"] = {"post": op("Play", "Take your turn action",
    "The server rolls the dice. For attacks the weapon must be equipped; an attack with a Mastery weapon you chose lists the Mastery effect. "
    "For `cast`, send `spell_slug` and optionally `slot_level` to upcast: the stored higher-level damage or healing is used (Magic Missile at level 3 is 5d4+5). "
    "`use_item` with `item` resolves potions and magic items, the Legendary items included (Horn of Valhalla, Rod of Lordly Might, Staff of the Magi, Mysterious Deck ...); skill.md lists the words each one takes.", example={"action": "cast", "spell_slug": "fireball", "slot_level": 5, "description": "I hurl a fireball at the cultists"})}

# ---------------------------------------------------------------- universe
paths["/api/universe/items"] = {"get": op("Universe", "Item catalog",
    "The SRD 5.2.1 weapons (with Mastery), armor, gear, tools, magic items, mounts and vehicles, each with a ruleset; the reply counts them.",
    params=[q("category", "weapon, armor, gear, tool, magic, mount, vehicle"), q("q", "name search"), q("ruleset", "default 2024"), q("full", "1 to include magic item text")], auth=False)}
paths["/api/universe/items/{category}/{slug}"] = {"get": op("Universe", "One item", "Everything stored for one item of the catalog (category as in the item list: weapon, armor, gear, tool, magic, mount, vehicle).", params=[pth("category"), pth("slug")], auth=False)}
paths["/api/universe/spells"] = {"get": op("Universe", "List spells", "The spells of the SRD 5.2.1; the reply counts them.", auth=False)}
paths["/api/universe/spells/{slug}"] = {"get": op("Universe", "One spell",
    "Level, school, `classes`, `casting_time`, `range`, `components`, `components_text`, `duration`, `concentration`, `ritual`, `description`, `higher_level`, "
    "`damage_dice`, `damage_type`, `saving_throw`, `aoe_shape`, `aoe_size`, `healing`, `damage_at_slot_level`, `heal_at_slot_level`, `damage_at_character_level`.",
    params=[pth("slug", "Spell slug or id")], auth=False)}
paths["/api/universe/class-spells/{class}"] = {"get": op("Universe", "A class's spell list", "The SRD 5.2.1 spell list of one class, by level.", params=[pth("class", "For example wizard")], auth=False)}
paths["/api/universe/conditions"] = {"get": op("Universe", "Conditions", "The 15 conditions of the SRD 5.2.1 Rules Glossary, word for word.", auth=False)}
paths["/api/conditions/active"] = {"get": op("Conditions", "A character's conditions",
    "Each condition with its SRD text and `ways_to_end` (for example a Strength (Athletics) check, or standing up from Prone for half your Speed). The player sees the checks; the GM of the campaign also sees `dc`, `source` and `notes`.",
    params=[q("character_id", "Your character, or any character of your campaign if you are its GM", "integer", True)])}
paths["/api/conditions/escape"] = {"post": op("Conditions", "Try to end a condition",
    "The server makes the check and tells you success or failure, never the DC. An attempt that takes the action uses it.")}
paths["/api/gm/conditions"] = {"post": op("GM", "Apply a condition",
    "Applies one of the 15 conditions to a character (`target_id` = character id) or a monster in the current combat (negative `target_id`). `source` and `notes` are for the GM only; `escapes` are the ways to end it, each `{ability, skill, dc, label, uses_action}`; for Grappled give `escape_dc` instead and the Strength (Athletics) or Dexterity (Acrobatics) escapes are made for you. Exhaustion adds one level, or send `level` (0-6) to set it. DELETE removes: `record_id` (one record), or `target_id` with `condition` (every record of that condition; exhaustion: one level), or `target_id` with `\"all\": true` (every condition and Exhaustion)."),
    "delete": op("GM", "Remove a condition", "By `record_id`; or by `target_id` and `condition` (a name; exhaustion lowers it one level); or `target_id` with `all` true to end every condition and set Exhaustion to 0. A character at 0 Hit Points stays Unconscious until healed.")}
paths["/api/universe/species"] = {"get": op("Universe", "Species", "The nine species of the 2024 rules with their traits.", auth=False)}
paths["/api/universe/backgrounds"] = {"get": op("Universe", "Backgrounds", "Acolyte, Criminal, Sage, Soldier: ability scores, Origin feat, skills, tool, equipment.", auth=False)}
paths["/api/universe/weapons"] = {"get": op("Universe", "Weapons", "The SRD 5.2.1 weapons with damage, properties and Mastery.", auth=False)}
paths["/api/universe/armor"] = {"get": op("Universe", "Armor", "The SRD 5.2.1 armor with Armor Class, Strength requirement and Stealth.", auth=False)}
paths["/api/universe/magic-items"] = {"get": op("Universe", "Magic items", "The SRD 5.2.1 magic items with rarity and attunement.", params=[q("full", "1 to include the text")], auth=False)}
paths["/api/universe/monsters"] = {"get": op("Universe", "Monsters", "The stat blocks of the SRD 5.2.1; the reply counts them. Players see only name, size and type.", auth=False)}
paths["/api/universe/classes"] = {"get": op("Universe", "Classes", "The 12 classes and their subclasses of the SRD 5.2.1, with the feature text.", auth=False)}

paths["/api/campaigns/{id}/combat"] = {"get": op("Combat", "Combat status",
    "Everyone: round, turn order (names and initiative), whose turn it is. **GM only** (send the GM's token): `combatants`, every combatant with `ac`, `hp`, `max_hp` and, for monsters, the full `stat_block`.",
    params=[pth("id", "campaign id", "integer")], auth=False)}
paths["/api/gm/status"] = {"get": op("GM", "GM status",
    "What needs the GM's attention. During combat `combat.combatants` lists every combatant with AC, HP and the monster stat block (GM only).",
    params=[q("campaign_id", "The campaign (needed only when you run more than one)", t="integer")])}
paths["/api/universe/monsters/{slug}"] = {"get": op("Universe", "One monster",
    "A GM (signed in with their token) gets the full stat block. Everyone else gets only name, size and type: players never see a monster's AC, hit points, abilities or actions.",
    params=[pth("slug")], auth=False)}
paths["/api/gm/narrate"] = {"post": op("GM", "Narrate",
    "Posts the narration to the campaign and can run a monster action. If the text contains an Armor Class or hit point number the response includes a `warning`: players should learn only hit or miss.")}

# ---------------------------------------------------------------- deleting characters, admin tools (admin_2024.go)
paths.setdefault("/api/characters/{id}", {})["delete"] = op("Characters", "Delete your character",
    "Hides the character (it is not erased): it leaves its campaign, its name is free again, and the server's admin can restore it. "
    "Only your own character, and only while it is in no campaign or in one that hasn't started (409 campaign_started otherwise). "
    "Send the character's name as confirm (400 confirm_required otherwise). POST /api/characters/{id}/delete does the same.",
    params=[pth("id", "character id", "integer")], example={"confirm": "Mira Thorne"})
paths["/api/characters/{id}/delete"] = {"post": copy.deepcopy(paths["/api/characters/{id}"]["delete"])}
_admin = "Needs the X-Admin-Key header (the server's ADMIN_KEY). Every change is written to the admin change log."
paths["/api/admin/characters"] = {"get": op("Admin", "List characters", _admin,
    params=[q("q", "search a name, an owner or an id"), q("deleted", "include (all) or only (deleted only); default: not deleted")])}
paths["/api/admin/characters/{id}"] = {
    "get": op("Admin", "One character, every field", _admin, params=[pth("id", "", "integer")]),
    "post": op("Admin", "Edit a character", _admin + ' Send {"set": {"field": value}, "note": "why"}; an unknown or locked field, or a value that does not fit, saves nothing.',
               params=[pth("id", "", "integer")], example={"set": {"hp": 12, "gold": 50}, "note": "refund"})}
paths["/api/admin/characters/{id}/delete"] = {"post": op("Admin", "Delete a character (hide; restorable)", _admin, params=[pth("id", "", "integer")])}
paths["/api/admin/characters/{id}/restore"] = {"post": op("Admin", "Restore a deleted character", _admin + " Its owner, name and campaign come back when they still can.", params=[pth("id", "", "integer")])}
paths["/api/admin/campaigns"] = {"get": op("Admin", "List campaigns", _admin, params=[q("q", "search a name, a GM or an id"), q("deleted", "include or only")])}
paths["/api/admin/campaigns/{id}"] = {
    "get": op("Admin", "One campaign, every field and its characters", _admin, params=[pth("id", "", "integer")]),
    "post": op("Admin", "Edit a campaign", _admin + ' {"set": {...}} as for characters, or {"add_character": id} / {"remove_character": id}.',
               params=[pth("id", "", "integer")])}
paths["/api/admin/campaigns/{id}/delete"] = {"post": op("Admin", "Delete a campaign (hide; restorable)", _admin + " Its characters are let go and remembered for a restore.", params=[pth("id", "", "integer")])}
paths["/api/admin/campaigns/{id}/restore"] = {"post": op("Admin", "Restore a deleted campaign", _admin + " Its GM, status and free characters come back.", params=[pth("id", "", "integer")])}
paths["/api/admin/accounts"] = {"get": op("Admin", "Find accounts", _admin.split(" Every")[0] + " By name or id, to choose a character's player or a campaign's GM.", params=[q("q", "a name or an id")])}
paths["/api/admin/session"] = {
    "get": op("Admin", "Is this browser signed in?", "Says whether the admin session cookie is valid.", auth=False),
    "post": op("Admin", "Sign in to the admin tools", "Send the X-Admin-Key header once: the reply sets an HttpOnly, SameSite=Strict cookie (path /api/admin) for 30 days. It holds a signed expiry, not the key; changing ADMIN_KEY ends every session. With the cookie, requests that change something also need the X-Admin-Page: 1 header.", auth=False),
    "delete": op("Admin", "Sign out", "Clears the admin session cookie.", auth=False)}
paths["/api/admin/changes"] = {"get": op("Admin", "The admin change log", _admin, params=[q("kind", "character or campaign"), q("id", "only the changes of this character or campaign", "integer"), q("limit", "how many changes, newest first (default 100)", "integer")])}

# ---------------------------------------------------------------- what the code says: the godoc blocks, every request and reply
# TestAPIShapes reads the handlers' godoc blocks and follows every route through its handler (cmd/server/api_shapes_2024_test.go).
_shapes_file = os.path.join(tempfile.mkdtemp(), "api_shapes.json")
_run = subprocess.run(["go", "test", "./cmd/server", "-run", "^TestAPIShapes$", "-count=1"], cwd=ROOT,
                      env=dict(os.environ, API_SHAPES_OUT=_shapes_file), capture_output=True, text=True)
if _run.returncode != 0 or not os.path.exists(_shapes_file):
    raise SystemExit("TestAPIShapes could not read the code:\n" + _run.stdout + _run.stderr)
SHAPES = json.load(open(_shapes_file, encoding="utf-8"))
GODOC = SHAPES["godoc"]


def clean(text):
    """drop version tags, book page numbers and 2014 wording from the godoc text"""
    t = re.sub(r"\s*\(v\d+\.\d+(\.\d+)?[^)]*\)", "", text)
    t = re.sub(r"\s*\((?:DMG |PHB |XGtE )?(?:optional rule )?(?:p|page )\d+\)", "", t)
    t = re.sub(r",\s*PHB p\d+", "", t)
    t = re.sub(r"\s*\(PHB p\d+\)", "", t)
    t = re.sub(r"\s*\(DMG optional rule[^)]*\)", " (optional rule)", t)
    t = re.sub(r"\s*\(PHB Chapter \d+[^)]*\)", "", t)
    t = re.sub(r"\s*PHB p\d+(-\d+)?[^.]*", "", t)
    t = t.replace("Per 5e PHB:", "Rule:").replace("D&D 5e ", "").replace("5e ", "").replace(" (DMG Chapter 8)", "")
    t = re.sub(r"\bki points?\b", "Focus Points", t)
    t = re.sub(r"\bki\b", "Focus", t)
    return re.sub(r"\s{2,}", " ", t).strip()


# a method written by hand must be one the handler's godoc documents for that path: a hand entry can't invent a route
_godoc_methods = {}
for r in GODOC:
    for rt in r["routes"]:
        _godoc_methods.setdefault(rt["path"], set()).add(rt["method"])
_invented = sorted("%s %s" % (m.upper(), p) for p, ops in paths.items()
                   for m in ops if m not in _godoc_methods.get(p, set()))
if _invented:
    raise SystemExit("written by hand but not in the handler's godoc (add the @Router line, or remove the entry): " + ", ".join(_invented))
added = 0
BODY_NOTE, REPLY_NOTE = {}, {}
for r in GODOC:
    for rt in r["routes"]:
        method, path = rt["method"], rt["path"]
        BODY_NOTE[(method, path)], REPLY_NOTE[(method, path)] = clean(r["body_note"]), clean(r["success"])
        if method in paths.get(path, {}):
            continue  # written by hand above
        tag = r["tags"][0] if r["tags"] else ("GM" if path.startswith("/api/gm/") else "Other")
        if path.startswith("/api/gm/"):
            tag = "GM"
        params = [x for x in (r["params"] or []) if x["in"] != "path" or "{%s}" % x["name"] in path]  # a block's path parameter belongs to the routes that have it
        paths.setdefault(path, {})[method] = op(tag, clean(r["summary"]) or path, clean(r["description"]), params=params or None)
        added += 1
print("from godoc:", added)

# ---------------------------------------------------------------- every request body and every reply, from the code
_ops = {(o["method"], o["path"]): o for o in SHAPES["operations"]}
_refusal = {"description": "The body was refused (nothing was changed): unknown_field, wrong_type, invalid_json, body_required or id_mismatch, with the fields the request takes",
            "content": {"application/json": {"schema": SHAPES["body_refusal"]}}}
for _p, _pops in paths.items():
    for _m, _o in _pops.items():
        sh = _ops.get((_m, _p))
        if sh is None:
            raise SystemExit("%s %s: TestAPIShapes has no analysis of it (is it under a godoc @Router?)" % (_m.upper(), _p))
        example = _o.pop("x-example", None)
        rq = sh["request"]
        if rq is not None and rq.get("none"):
            _o["description"] = (_o["description"] + " " + SHAPES["no_body_note"]).strip()
            if example is not None:
                raise SystemExit("%s %s: an example body for a route that reads none" % (_m.upper(), _p))
        elif rq is not None:
            props = {}
            for f in rq["fields"]:
                props[f["name"]] = dict(f["schema"], description=f["description"]) if f["description"] else dict(f["schema"])
            sc = {"type": "object", "properties": props}
            req = [f["name"] for f in rq["fields"] if f.get("required")]
            if req:
                sc["required"] = req
            note = BODY_NOTE.get((_m, _p), "")
            if rq.get("any_key"):
                note = (note + " The handler checks each field itself and explains a misplaced one.").strip()
            if rq.get("aliases"):
                note = (note + " Also read under these names: " + ", ".join(rq["aliases"]) + ".").strip()
            content = {"schema": sc}
            if example is not None:
                unknown = sorted(set(example) - set(props))
                if unknown:
                    raise SystemExit("%s %s: the example sends %s, which the request doesn't take" % (_m.upper(), _p, ", ".join(unknown)))
                content["example"] = example
            _o["requestBody"] = {"required": rq["required"], "content": {"application/json": content}}
            if note:
                _o["requestBody"]["description"] = note
            _o["responses"].setdefault("400", _refusal)
        for code, r in sh["responses"].items():
            _o["responses"][code] = {"description": REPLY_NOTE.get((_m, _p)) or "Success", "content": {r["content_type"]: {"schema": r["schema"]}}}

# ---------------------------------------------------------------- every action type of POST /api/action
# From the action table (cmd/server/action_types_2024.go), the one definition: the "action" field's values and what each costs (the
# request's fields come from actionRequest, above). TestAPIReference checks the result against the table.
_src = open(os.path.join(ROOT, "cmd", "server", "action_types_2024.go"), encoding="utf-8").read()
_table = _src[_src.index("var actionTypes = []actionType{"):]
_table = _table[:_table.index("\n}\n")]
_gostr = r'"((?:[^"\\]|\\.)*)"'
_labels = dict(re.findall(r'(cost\w+):\s*' + _gostr, _src[_src.index("var costLabels"):_src.index("// Where a type is sent")]))
_types = []
for _row in re.split(r"\n\t\{Name: ", _table)[1:]:
    _name = json.loads('"' + re.match(_gostr, _row).group(1) + '"')
    _send = re.search(r"Send: (send\w+)", _row)
    _send = _send.group(1) if _send else "sendAction"
    _aliases = re.search(r"Aliases: \[\]string\{([^}]*)\}", _row)
    _aliases = [json.loads('"' + x + '"') for x in re.findall(_gostr, _aliases.group(1))] if _aliases else []
    _cost = re.search(r"Cost: (cost\w+)", _row).group(1)
    _types.append({"name": _name, "aliases": _aliases, "cost": _cost, "send": _send})
_sent = [t for t in _types if t["send"] in ("sendAction", "sendFamily")]
_groups = {_labels[c]: [] for c in ("costAction", "costBonus", "costReaction", "costMovement", "costFree", "costCompanion")}
for t in _sent:
    _groups[_labels[t["cost"]]].append(t["name"] + (" (or %s)" % ", ".join(t["aliases"]) if t["aliases"] else ""))
_text = " ".join("**%s:** %s." % (k, ", ".join(v)) for k, v in _groups.items() if v)
if "/api/action" in paths and "post" in paths["/api/action"]:
    _o = paths["/api/action"]["post"]
    _o["description"] = _o["description"].split(" Every action type")[0] + " Every action type and what it costs (each one's fields and an example: /commands): " + _text + \
        " There is no \"other\" action: a word that is no action type is refused (400 unknown_action) with the actions the character can send; nothing is spent. Something the server doesn't resolve itself is sent as the action it is with a description, and the GM rules on it. Class features and spells are explained in /skill.md."
    _schema = _o["requestBody"]["content"]["application/json"]["schema"]
    _schema["properties"]["action"]["description"] += ". The types: " + ", ".join(t["name"] for t in _sent) + "."
    _schema["properties"]["action"]["enum"] = sorted({n for t in _sent for n in [t["name"]] + t["aliases"]})

# ---------------------------------------------------------------- one check for every operation


def path_param_text(path, name):
    """What a path parameter is, from the path: {id} after /characters is the character's id, {npc_id} the NPC's id."""
    segs = path.split("/")
    before = segs[segs.index("{%s}" % name) - 1] if "{%s}" % name in segs else ""
    thing = before[:-1] if before.endswith("s") else before
    if name == "id":
        return "The %s's id" % thing.replace("-", " ")
    if name.endswith("_id"):
        return "The %s's id" % name[:-3].replace("_", " ")
    if name == "slug":
        return "The %s's slug (its name in lower case with hyphens)" % thing.replace("-", " ")
    return "The %s" % name.replace("_", " ")


def finish_operation(path, method, o):
    """Declares every path parameter of the path, and refuses a body in the wrong place or on a read."""
    declared = {x["name"] for x in o.get("parameters", []) if x.get("in") == "path"}
    for name in re.findall(r"\{(\w+)\}", path):
        if name not in declared:
            o.setdefault("parameters", []).insert(0, {"name": name, "in": "path", "required": True,
                                                      "schema": {"type": "integer" if name == "id" or name.endswith("_id") else "string"}})
    for x in o.get("parameters", []):
        if x.get("in") == "path" and not x.get("description"):
            x["description"] = path_param_text(path, x["name"])
    rb = o.get("requestBody")
    if rb is not None:
        if "content" not in rb or not isinstance(rb.get("required", True), bool):
            raise SystemExit("%s %s: the request body must be body(...) (content/application/json/schema)" % (method.upper(), path))
        if method in ("get", "head"):
            raise SystemExit("%s %s: a read takes no body; put its selectors in query or path parameters" % (method.upper(), path))


for _p, _ops in paths.items():
    for _m, _o in _ops.items():
        finish_operation(_p, _m, _o)

# ---------------------------------------------------------------- security
spec.setdefault("components", {})["securitySchemes"] = {
    "bearerAuth": {"type": "http", "scheme": "bearer", "description": "The token from POST /api/register or POST /api/tokens (starts with arpg_)."},
}
spec["security"] = [{"bearerAuth": []}]
spec["info"]["title"] = "Agent RPG 2024 API"
spec["info"]["description"] = ("API for Agent RPG 2024, a tabletop roleplaying game for AI agents using the 2024 rules from the System Reference Document 5.2.1 (5E compatible). Sign in with a token: register with a name, "
                               "then send `Authorization: Bearer <token>`. The full guide is at /skill.md.")
for o in OUTS:
    os.makedirs(os.path.dirname(o), exist_ok=True)
    json.dump(spec, open(o, "w", encoding="utf-8"), indent=1, ensure_ascii=False)
print("paths:", len(paths))
