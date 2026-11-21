# Monster acceptance ledger

The selected-mechanics test imports each listed monster and runs an encounter through the server routes. Its checks cover the attacks, saves and damage behavior asserted by that test. Twenty selected-mechanics passes do not establish twenty every-ability passes.

| Monster | Source in the selected-mechanics roster | Every-ability review |
| --- | --- | --- |
| Balor | SRD 5.2.1 | Open |
| Ancient Red Dragon | SRD 5.2.1 | Open |
| Ancient Gold Dragon | SRD 5.2.1 | Open |
| Ancient Blue Dragon | SRD 5.2.1 | Open |
| Ancient Silver Dragon | SRD 5.2.1 | Open |
| Dragon Turtle | SRD 5.2.1 | Open |
| Ice Devil | SRD 5.2.1 | Open |
| Iron Golem | SRD 5.2.1 | Seven named entries exercised on the supplied reconstruction. Full scope remains open. |
| Kraken | SRD 5.2.1 | Open |
| Lich | SRD 5.2.1 | Open |
| Marilith | SRD 5.2.1 | Open |
| Mummy Lord | SRD 5.2.1 | Open |
| Pit Fiend | SRD 5.2.1 | Open |
| Planetar | SRD 5.2.1 | Open |
| Solar | SRD 5.2.1 | Open |
| Tarrasque | SRD 5.2.1 | Open |
| Death Knight | Supplied private reconstruction | Open |
| Arch-hag | Supplied private reconstruction | Open |
| Dracolich | Supplied private reconstruction | Open |
| Elemental Cataclysm | Supplied private reconstruction | Open |

## Fresh replacement attempts

| Candidate | Code on first run | Result | Follow-up |
| --- | --- | --- | --- |
| Abominable Yeti (CR 9, supplied reconstruction) | `16d3c4c4a3d8a1141ef9eb067e236b0152f64d81` | Failed. Fire damage omitted the named attack penalty. Gaze incorrectly paralyzed a cold-immune target and used the wrong immunity duration. | These limited checks passed after repair. A further route probe found range, count and sight defects. Named range and Recharge checks passed in a separate successor test. Complete ability coverage remains open. |
| Brazen Gorgon (CR 9, supplied reconstruction) | `96807f0fc6ac2255979682b51894cce668adbca5` | Failed. A movement-triggered save rolled and spent Recharge without the required move. | Gore, Multiattack, Flame Aura, Illumination, movement refusal and the failed-save GM charge workflow passed scoped follow-ups. The successful-save, additional-target and grapple-occupancy cases remain open. |
| Gray Slaad (CR 9, supplied reconstruction) | `b47bdc7d779a42ecc9863d92aa4e287fd7a14f9b` | Failed. Self-only Invisibility left the caster visible, and changing shape did not change size. | Scoped Regeneration, Magic Resistance, Claw and Multiattack probes passed. All nine spell choices were reached. Focused retests of the two fixes and complete spell and rider workflow testing are open. |

A first-run result stays in this ledger after a fix. A passing rerun of the same monster verifies the repair and does not count as a fresh replacement. Before counting a candidate, verify its abilities and interactions through the applicable server routes or the supported GM workflow, with the resulting game state checked.

The reference fixture bytes are private inputs. See the named ability notes for the rules sources, route assertions, mutations and remaining work.
