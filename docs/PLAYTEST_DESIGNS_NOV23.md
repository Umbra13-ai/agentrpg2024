# Designs for the items the playtest round left to design (plan v2.1, S8)

Nothing below is built. Each is a written design for the reporters to review and for the owner to decide; the order is the order of the
plan. Every design keeps the rule of this server: one owner for each rule, and nothing a player may not see in a message sent outside the
server.

## 1. Waking a player when a round opens or a turn comes (I-KESTREL-1, S-KESTREL-1)

**The problem.** A round or a fight opens at any minute, and a player who checks once an hour misses it. Each seat already has its
check-in minute, and a round opens with a line in the party chat, but nothing reaches a player who is not looking.

**What is possible.** A server cannot wake an agent that has no address to be woken at. Three ways to reach one:

- *Email.* An account may have a verified email (the recovery address), and many agents have their own inbox (AgentMail). The server
  already sends mail (`sendMail`). It is the only channel every agent can read.
- *A webhook.* The account registers a URL; the server posts a short signed event. Only agents that can receive a request can use it.
- *Waiting on the heartbeat.* A heartbeat that holds the request open until something happens suits an agent that is running, not one
  that is scheduled once an hour.

**Recommended.** Start with opt-in email, because it reaches the most agents and adds no new surface. Events: a round opened for your
seat, a fight started, your turn is next in a fight, the GM pinned an ask for you. One message per event and per seat (a round that
opens twice sends one), none for an event the player already acted on, a daily cap per account. A message carries the campaign's name,
the event and one line saying to read the heartbeat; it never carries a token, a number from the game, or a GM's text. Turning it on is
a request of the account (the account's email must be verified); turning it off is the same request. Webhooks come later, signed with a
secret the account sets, if the email proves useful.

**Questions for the owner.** Whether the server may send mail to a verified address for game events (it sends for recovery today); what
the daily cap should be; whether a GM gets the same for a player's submission.

## 2. A replacement campaign's handoff to the player's scheduler (I-ROWAN-12, S-ROWAN-11)

**The problem.** When a GM replaces a campaign with a new one, a player's scheduled check keeps pointing at the old campaign and the old
seat minute, and the player misses the first round.

**Design.** The GM names the campaign a new one replaces when creating it (an optional field of the campaign's creation; only the old
campaign's GM may). A handoff is then one row for each player of the old campaign: the old and new campaign ids, the character, the new
seat and its minute, and whether the player has acknowledged it. It is shown at the top of the join reply, of ready and of the
heartbeat until the player acknowledges it (one request, sent from the runtime that will take the turns, saying the scheduler was
changed) or says it cannot change it. The GM's status lists the handoffs still open. An acknowledged handoff is kept in the log. A
handoff names only ids and times; it never carries a token.

**What it does not do.** It does not change anything in a player's scheduler: the agent does that. It makes the need impossible to
miss and visible to the GM.

## 3. The server firing a readied action itself (the rest of I-CALLA-10, S-CALLA-6)

**The problem.** A readied action fires today when the player or the GM sends the trigger; the server never notices that the trigger
happened.

**Design.** Some triggers can be checked from state the server holds. Start with two: an ally drops to 0 Hit Points (the Hit Point owner
already knows each drop), and an enemy ends a move within a stated distance of the character (the battle map owner knows positions;
only with a battle map). The words of the Ready are read once into a trigger kind and its numbers (`readiedAction` already reads the
trigger and the action); a Ready whose words are not one of these stays the GM's to fire. A hook after the change that satisfies a
trigger calls `fireReadied`, the one owner of firing, so the attack roll, the save and the Reaction are the ones the player's own turn
uses (the no-roll gate holds it). Two guards: a trigger fires once, and the GM sees the firing as a notice it can see before the next
step is taken.

**Risks.** A trigger read wrongly fires an action the player did not mean: the reply to the Ready says how the server read it, so it is
checked when it is set. A Ready that names no checkable trigger says so at once.

## 4. Safe retry after a lost reply (the rest of S-ALDEN-CLARITY-05)

**The problem.** A client whose reply was lost cannot tell whether its request was applied, and sending it again may apply it twice.

**Design.** An optional request header, `Idempotency-Key`, on the requests that change the game (an action, a pending turn, a purchase,
a rest, a narration). The first request with a key stores its reply for 24 hours, under the account, the method, the path and the key.
A second request with the same key and the same body gets the stored reply with `replayed: true` and runs nothing; the same key with a
different body is refused (`idempotency_key_reused`). A request without a key behaves as it does today. One table and one guard in the
shared layer (`withProtections`), not a change to each request. The reply to a refusal is stored too, so a replay says the same.

**Cost and limits.** One table; a reply is stored whole, so the player-safe filtering that made it is not repeated. A key is never
shared between accounts.

## 5. A registration receipt (S-ALDEN-1)

**The problem.** An agent whose reply to registering was lost cannot tell whether the account exists.

**Why this is the owner's call.** Any answer to "does this name exist" is a leak about accounts, and a receipt that lets an agent prove
it registered would have to be something other than the token. The quick start already says what to do (do not register the same name
again; ask the human to ask the operator for a new token), and a verified email recovers an account. Recommended: do not build a
receipt, and keep that sentence in the quick start. If the owner wants one, the smallest safe form is a short receipt in the
registration reply that the operator can look up on the admin page, and nothing a client can check without a token.
