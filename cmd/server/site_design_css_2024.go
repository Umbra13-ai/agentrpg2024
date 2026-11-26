package main

// designCSS: the content components of the Nov 21 mock-up (data/ui_preview/gui-2026-11-21/index.html: panels, buttons, badges, the
// character sheet, the feed, the accordion, the modal), on the theme variables of themes_2024.go, which give them the Nov 25 design's
// colors. Its page chrome (top bar, tab bar, workspace) is gone: the page shell is site_layout_2024.go's (owner, Nov 26). Two changes only, for the site's floors: text is never under 12 px (the mock-up's 0.6-0.72rem
// sizes are 0.75rem), and touch targets are 40 px on touch screens (siteCSS). Never edit a part of the design here to fit one page:
// a page adds its own CSS.
const designCSS = `
* { box-sizing: border-box; margin: 0; padding: 0; }
html, body {
  max-width: 100%;
  overflow-x: hidden;
  background-color: var(--bg-outer);
  color: var(--text-primary);
  font-family: 'Inter', -apple-system, BlinkMacSystemFont, sans-serif;
  line-height: 1.5;
  -webkit-font-smoothing: antialiased;
}
.font-rpg { font-family: 'Cinzel', Georgia, serif; letter-spacing: 0.03em; }
.font-mono { font-family: 'JetBrains Mono', ui-monospace, monospace; }
/* Ornate RPG Frame */
.rpg-panel {
  position: relative;
  background: var(--bg-surface);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-rpg);
  overflow: hidden;
}
.rpg-panel::before {
  content: "";
  position: absolute;
  inset: 0;
  pointer-events: none;
  border-radius: inherit;
  background: linear-gradient(135deg, var(--panel-sheen) 0%, transparent 35%, transparent 65%, var(--panel-sheen) 100%);
}
.panel-pad { padding: 1rem; }
@media (min-width: 768px) { .panel-pad { padding: 1.25rem; } }
.btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 0.4rem;
  padding: 0.5rem 0.9rem;
  border-radius: var(--radius-sm);
  font-size: 0.82rem;
  font-weight: 600;
  cursor: pointer;
  border: 1px solid transparent;
  transition: all 0.16s ease;
  min-height: 38px;
  font-family: inherit;
  white-space: nowrap;
}
.btn-gold {
  background: var(--btn-accent-bg);
  color: var(--btn-accent-text);
  border-color: var(--border-strong);
  box-shadow: 0 4px 16px var(--accent-gold-soft);
}
.btn-gold:hover { filter: brightness(1.07); transform: translateY(-1px); }
.btn-ghost {
  background: var(--bg-elevated);
  color: var(--text-primary);
  border-color: var(--border-subtle);
}
.btn-ghost:hover { border-color: var(--border-strong); }
.btn-sm { padding: 0.32rem 0.68rem; font-size: 0.76rem; min-height: 32px; }
/* Badges */
.badge {
  display: inline-flex;
  align-items: center;
  gap: 0.28rem;
  padding: 0.18rem 0.55rem;
  border-radius: 999px;
  font-size: 0.75rem;
  font-weight: 700;
  letter-spacing: 0.03em;
  text-transform: uppercase;
  white-space: nowrap;
}
.badge-active { background: var(--accent-emerald-soft); color: var(--accent-emerald); border: 1px solid color-mix(in srgb, var(--accent-emerald) 40%, transparent); }
.badge-combat { background: var(--accent-crimson-soft); color: var(--accent-crimson); border: 1px solid color-mix(in srgb, var(--accent-crimson) 40%, transparent); }
.badge-recruiting { background: var(--accent-gold-soft); color: var(--accent-gold); border: 1px solid color-mix(in srgb, var(--accent-gold) 45%, transparent); }
.badge-arcane { background: var(--accent-arcane-soft); color: var(--accent-arcane); border: 1px solid color-mix(in srgb, var(--accent-arcane) 40%, transparent); }
/* Interactive Dice Tray */
.dice-tray {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.45rem;
  padding: 0.75rem;
  background: var(--bg-elevated);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-md);
  margin-bottom: 0.9rem;
}
.die-btn {
  background: var(--bg-code);
  color: var(--accent-gold);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-sm);
  padding: 0.35rem 0.62rem;
  font-size: 0.78rem;
  font-weight: 700;
  cursor: pointer;
  transition: all 0.14s ease;
  min-height: 34px;
}
.die-btn:hover {
  border-color: var(--accent-gold);
  background: var(--accent-gold-soft);
  transform: translateY(-1px);
}
/* Chronicle / Activity Feed */
.feed-entry {
  border-left: 3px solid var(--accent-emerald);
  background: var(--bg-elevated);
  border-radius: 0 var(--radius-md) var(--radius-md) 0;
  padding: 0.85rem 1rem;
  margin-bottom: 0.7rem;
}
.feed-entry.type-narration { border-left-color: var(--accent-arcane); }
.feed-entry.type-combat { border-left-color: var(--accent-crimson); }
.feed-entry.type-search { border-left-color: var(--accent-gold); }
.roll-pill {
  display: inline-flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.4rem;
  max-width: 100%;
  word-break: break-word;
  background: var(--bg-code);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-sm);
  padding: 0.3rem 0.6rem;
  margin-top: 0.45rem;
  font-size: 0.75rem;
  color: var(--accent-gold);
}
.feed-entry {
  max-width: 100%;
  overflow-wrap: anywhere;
}
/* Grids & Inputs */
.grid-2 { display: grid; grid-template-columns: 1fr; gap: 1rem; }
@media (min-width: 768px) { .grid-2 { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
.grid-4 { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 0.75rem; }
@media (min-width: 1024px) { .grid-4 { grid-template-columns: repeat(4, minmax(0, 1fr)); } }
.filter-row { display: flex; flex-wrap: wrap; gap: 0.45rem; align-items: center; }
.pill-btn {
  background: var(--bg-elevated);
  color: var(--text-secondary);
  border: 1px solid var(--border-subtle);
  border-radius: 999px;
  padding: 0.32rem 0.75rem;
  font-size: 0.76rem;
  font-weight: 600;
  cursor: pointer;
  min-height: 32px;
  font-family: inherit;
}
.pill-btn.active {
  background: var(--accent-gold-soft);
  color: var(--accent-gold);
  border-color: var(--accent-gold);
}
.input-field {
  width: 100%;
  background: var(--bg-code);
  color: var(--text-primary);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-sm);
  padding: 0.52rem 0.8rem;
  font-size: 0.86rem;
  font-family: inherit;
  min-height: 40px;
}
.input-field:focus { outline: none; border-color: var(--accent-gold); }
/* Character Sheet */
.char-hero {
  display: flex;
  flex-direction: row;
  align-items: flex-start;
  gap: 0.85rem;
  margin-bottom: 0.75rem;
}
.char-portrait-col {
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
  flex-shrink: 0;
  width: 82px;
}
@media (min-width: 768px) {
  .char-portrait-col {
    width: 108px;
  }
}
.char-portrait-frame {
  width: 100%;
  height: 82px;
  border-radius: 12px;
  border: 2px solid var(--border-strong);
  overflow: hidden;
  background: var(--bg-elevated);
  display: grid;
  place-items: center;
  box-shadow: 0 0 18px var(--accent-gold-soft);
}
@media (min-width: 768px) {
  .char-portrait-frame {
    height: 108px;
  }
}
.portrait-turn-badge {
  width: 100%;
  justify-content: center;
  text-align: center;
  padding: 0.22rem 0.2rem;
  font-size: 0.75rem;
}
/* Compact 3x2 Vitals Strip */
.vitals-strip {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 0.45rem;
  margin-bottom: 0.9rem;
}
@media (min-width: 768px) {
  .vitals-strip {
    grid-template-columns: repeat(6, minmax(0, 1fr));
  }
}
/* 3x2 Ability Score Shields */
.abilities-3x2 {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 0.55rem;
  margin-bottom: 1rem;
}
.ddb-ability {
  position: relative;
  background: linear-gradient(180deg, var(--bg-elevated) 0%, var(--bg-code) 100%);
  border: 1.5px solid var(--border-strong);
  border-radius: 12px;
  padding: 0.5rem 0.35rem 0.45rem 0.35rem;
  text-align: center;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 0.3rem;
  box-shadow: inset 0 0 12px var(--panel-sheen);
  min-width: 0;
}
.ddb-ability-name {
  font-size: 0.75rem;
  font-weight: 700;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--text-secondary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 100%;
}
.ddb-ability-mod {
  width: 76%;
  padding: 0.22rem 0.2rem;
  border: 1px solid var(--border-subtle);
  border-radius: 6px;
  background: color-mix(in srgb, var(--bg-app) 55%, transparent);
  font-size: 1.25rem;
  font-weight: 700;
  color: var(--text-primary);
  line-height: 1.15;
}
.ddb-ability-score {
  min-width: 44px;
  padding: 0.14rem 0.55rem;
  border-radius: 999px;
  border: 1.5px solid var(--border-strong);
  background: var(--bg-surface);
  font-size: 0.82rem;
  font-weight: 700;
  color: var(--accent-gold);
}
/* Saving Throws 2-Col x 3-Row */
.saves-section-title {
  font-size: 0.92rem;
  font-weight: 700;
  color: var(--accent-gold);
  margin: 0.85rem 0 0.5rem 0;
  padding-top: 0.75rem;
  border-top: 1px solid var(--border-subtle);
}
.saves-2col {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0.45rem 0.6rem;
}
.save-pill {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.35rem;
  background: var(--bg-elevated);
  border: 1px solid var(--border-subtle);
  border-radius: 999px;
  padding: 0.28rem 0.3rem 0.28rem 0.5rem;
  min-width: 0;
}
.save-dot {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  border: 1.5px dotted var(--text-muted);
  flex-shrink: 0;
}
.save-dot.prof {
  border: 1.5px solid var(--accent-gold);
  background: var(--accent-gold);
  box-shadow: 0 0 8px var(--accent-gold-soft);
}
.save-name {
  font-size: 0.75rem;
  font-weight: 700;
  letter-spacing: 0.03em;
  text-transform: uppercase;
  color: var(--text-secondary);
  flex: 1;
  text-align: center;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.save-val {
  min-width: 34px;
  text-align: center;
  padding: 0.16rem 0.35rem;
  border-radius: 8px;
  background: var(--bg-code);
  border: 1px solid var(--border-strong);
  font-size: 0.8rem;
  font-weight: 700;
  color: var(--text-primary);
  flex-shrink: 0;
}
/* Senses Stacked Pills */
.senses-stack {
  display: flex;
  flex-direction: column;
  gap: 0.45rem;
}
.sense-pill {
  display: flex;
  align-items: center;
  gap: 0.7rem;
  background: var(--bg-elevated);
  border: 1px solid var(--border-subtle);
  border-radius: 999px;
  padding: 0.25rem 0.85rem 0.25rem 0.25rem;
  min-width: 0;
}
.sense-num {
  min-width: 42px;
  height: 32px;
  display: grid;
  place-items: center;
  border-radius: 999px;
  background: var(--bg-code);
  border: 1.5px solid var(--border-strong);
  font-size: 0.9rem;
  font-weight: 700;
  color: var(--accent-gold);
  flex-shrink: 0;
}
.sense-lbl {
  font-size: 0.75rem;
  font-weight: 700;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--text-secondary);
}
.mini-vital {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.3rem;
  background: var(--bg-elevated);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-sm);
  padding: 0.28rem 0.5rem;
  min-width: 0;
}
.mini-vital-lbl {
  font-size: 0.75rem;
  font-weight: 700;
  color: var(--text-muted);
  text-transform: uppercase;
  white-space: nowrap;
}
.mini-vital-val {
  font-size: 0.78rem;
  font-weight: 700;
  color: var(--text-primary);
  white-space: nowrap;
}
.char-portrait-frame img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.char-vitals-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 0.5rem;
  margin-top: 0.75rem;
}
@media (min-width: 768px) {
  .char-vitals-grid {
    grid-template-columns: repeat(6, minmax(0, 1fr));
  }
}
.char-tabs {
  display: flex;
  flex-wrap: wrap;
  gap: 0.45rem;
  margin-bottom: 1rem;
}
.slot-pip {
  display: inline-block;
  width: 12px;
  height: 12px;
  border-radius: 3px;
  border: 1px solid var(--accent-gold);
  background: var(--accent-gold);
  margin-right: 4px;
}
.slot-pip.empty {
  background: transparent;
}
.dnd-list-item {
  padding: 0.6rem 0.75rem;
  background: var(--bg-elevated);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-sm);
  font-size: 0.83rem;
  max-width: 100%;
  overflow-wrap: anywhere;
}
.char-grid { display: grid; grid-template-columns: minmax(0, 1fr); gap: 1rem; } .char-grid > * { min-width: 0; max-width: 100%; }
@media (min-width: 900px) { .char-grid { grid-template-columns: 1.05fr 0.95fr; } }
.stat-box-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 0.55rem; }
.stat-box {
  background: var(--bg-elevated);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-md);
  padding: 0.6rem 0.45rem;
  text-align: center;
}
.ability-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 0.55rem; }
@media (min-width: 640px) { .ability-grid { grid-template-columns: repeat(6, minmax(0, 1fr)); } }
.ability-shield {
  background: linear-gradient(180deg, var(--bg-elevated), var(--bg-code));
  border: 1px solid var(--border-strong);
  border-radius: 10px 10px 16px 16px;
  padding: 0.55rem 0.35rem;
  text-align: center;
}
/* Accordion & Code */
.accordion-btn {
  width: 100%;
  background: var(--bg-elevated);
  color: var(--text-primary);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-md);
  padding: 0.72rem 0.9rem;
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 0.86rem;
  font-weight: 600;
  cursor: pointer;
  font-family: inherit;
}
.accordion-body {
  padding: 0.8rem 0.9rem;
  background: var(--bg-code);
  border: 1px solid var(--border-subtle);
  border-top: none;
  border-radius: 0 0 var(--radius-md) var(--radius-md);
  font-size: 0.82rem;
  color: var(--text-secondary);
}
pre.code-block {
  background: var(--bg-code);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-sm);
  padding: 0.75rem;
  font-size: 0.76rem;
  overflow-x: auto;
  max-width: 100%;
  color: var(--text-primary);
}
/* Modal & Toast */
.modal-backdrop {
  position: fixed;
  inset: 0;
  z-index: 65;
  background: rgba(0, 0, 0, 0.76);
  display: none;
  align-items: center;
  justify-content: center;
  padding: 1rem;
}
.modal-backdrop.open { display: flex; }
.modal-panel {
  width: 100%;
  max-width: 520px;
  background: var(--bg-surface);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-lg);
  padding: 1.25rem;
  box-shadow: var(--shadow-rpg);
  max-height: 90vh;
  overflow-y: auto;
}
.toast {
  position: fixed;
  bottom: 5rem;
  right: 1rem;
  z-index: 75;
  background: var(--bg-elevated);
  color: var(--accent-gold);
  border: 1px solid var(--accent-gold);
  border-radius: var(--radius-md);
  padding: 0.6rem 0.95rem;
  font-size: 0.82rem;
  font-weight: 600;
  box-shadow: var(--shadow-rpg);
  opacity: 0;
  pointer-events: none;
  transform: translateY(10px);
  transition: all 0.2s ease;
}
@media (min-width: 1024px) { .toast { bottom: 1.5rem; } }
.toast.show { opacity: 1; transform: translateY(0); }
.hidden { display: none !important; }
`
