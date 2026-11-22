// The pages at a phone's width (CLAUDE.md, the five views: "text never under 12 px, a touch target never under 40 px, no horizontal scroll from 320 px").
// Usage: BASE=http://localhost:8099 node tools/page_check_320.js [path ...]   (Chromium and Playwright are the ones installed here: no install needed)
// Without paths it opens the pages named below, then follows every internal link of the five views once (up to MAX_PAGES) and prints the failing list:
//   scroll   the page is wider than 320 px (document scroll width)
//   text     a visible text node under 12 px
//   target   a visible link, button, input, select or summary under 40 px high (the site's CSS gives a control min-height 40 px; a short word is
//            narrower than that and is fine). A link inside running text is not one: only a link that is alone in its block, or a control, counts
// Exit status 1 when anything fails. ADMIN_PATH=/secret adds the admin page; with ADMIN_KEY too it signs in and checks the page's tabs and the first
// character's editor.
const { chromium } = require('playwright');
const BASE = (process.env.BASE || 'http://localhost:8099').replace(/\/$/, '');
const MAX = parseInt(process.env.MAX_PAGES || '60', 10);
const START = process.argv.length > 2 ? process.argv.slice(2) : ['/', '/live', '/campaigns', '/character', '/universe', '/commands', '/docs', '/start'].concat(process.env.ADMIN_PATH ? [process.env.ADMIN_PATH] : []);

async function audit(page) {
  return page.evaluate(() => {
    const out = { scroll: null, text: [], target: [] };
    const w = document.documentElement.clientWidth;
    if (document.documentElement.scrollWidth > w + 1) {
      // the widest culprits
      const wide = [...document.querySelectorAll('body *')].filter(e => e.getBoundingClientRect().right > w + 1 && getComputedStyle(e).position !== 'fixed').slice(0, 4).map(e => e.tagName.toLowerCase() + (e.className && typeof e.className === 'string' ? '.' + e.className.split(' ')[0] : ''));
      out.scroll = `scroll width ${document.documentElement.scrollWidth} > ${w}: ${wide.join(', ')}`;
    }
    const visible = e => { const r = e.getBoundingClientRect(); const s = getComputedStyle(e); return r.width > 0 && r.height > 0 && s.visibility !== 'hidden' && s.display !== 'none' && parseFloat(s.opacity) > 0; };
    const seen = new Set();
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    for (let n = walker.nextNode(); n; n = walker.nextNode()) {
      if (!n.nodeValue.trim()) continue;
      const e = n.parentElement;
      if (!e || ['SCRIPT', 'STYLE', 'NOSCRIPT'].includes(e.tagName) || !visible(e)) continue;
      const px = parseFloat(getComputedStyle(e).fontSize);
      if (px < 12) { const k = e.tagName + '.' + e.className + px; if (!seen.has(k)) { seen.add(k); out.text.push(`${px}px ${e.tagName.toLowerCase()}.${String(e.className).split(' ')[0]}: "${n.nodeValue.trim().slice(0, 40)}"`); } }
    }
    for (const e of document.querySelectorAll('a[href], button, input:not([type=hidden]), select, summary, [role=button], textarea')) {
      if (!visible(e)) continue;
      let r = e.getBoundingClientRect();
      if ((e.type === 'checkbox' || e.type === 'radio') && e.closest('label')) r = e.closest('label').getBoundingClientRect(); // its label is the target
      if (e.tagName === 'A' && getComputedStyle(e).display === 'inline') { // a link in running text
        const block = e.parentElement; const rest = (block.textContent || '').replace(e.textContent || '', '').trim();
        if (rest.length > 0) continue;
      }
      if (r.height < 39.5) out.target.push(`${Math.round(r.width)}x${Math.round(r.height)} ${e.tagName.toLowerCase()}.${String(e.className).split(' ')[0]}: "${(e.textContent || e.value || e.getAttribute('aria-label') || '').trim().slice(0, 30)}"`);
    }
    out.links = [...document.querySelectorAll('a[href]')].map(a => a.getAttribute('href')).filter(h => h && h.startsWith('/') && !h.startsWith('//'));
    return out;
  });
}

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 320, height: 700 }, deviceScaleFactor: 1 });
  const page = await ctx.newPage();
  const queue = START.slice(), seen = new Set(), bad = [];
  let n = 0;
  while (queue.length && n < MAX) {
    const path = queue.shift();
    const key = path.split('#')[0];
    if (seen.has(key) || /^\/(api|assets|preview|health)/.test(key) || /\.(md|json|png|jpg|webp|svg|txt|xml|ico|css|js)$/.test(key)) continue;
    seen.add(key);
    n++;
    let res;
    try { res = await page.goto(BASE + key, { waitUntil: 'networkidle', timeout: 20000 }); } catch (e) { bad.push(`${key}: did not load: ${e.message.split('\n')[0]}`); continue; }
    if (!res || res.status() >= 400) { bad.push(`${key}: status ${res && res.status()}`); continue; }
    const r = await audit(page);
    if (r.scroll) bad.push(`${key}: scroll: ${r.scroll}`);
    for (const t of r.text) bad.push(`${key}: text: ${t}`);
    for (const t of r.target) bad.push(`${key}: target: ${t}`);
    if (START.length === 8 || process.argv.length <= 2) for (const l of r.links) if (!seen.has(l.split('#')[0]) && !queue.includes(l)) queue.push(l);
  }
  if (process.env.ADMIN_PATH && process.env.ADMIN_KEY) { // the signed-in admin page: its tabs and a character's card
    await ctx.request.post(BASE + '/api/admin/session', { headers: { 'X-Admin-Key': process.env.ADMIN_KEY } });
    await page.goto(BASE + process.env.ADMIN_PATH, { waitUntil: 'networkidle' });
    const look = async (what) => {
      const r = await audit(page);
      if (r.scroll) bad.push(`admin ${what}: scroll: ${r.scroll}`);
      for (const t of r.text) bad.push(`admin ${what}: text: ${t}`);
      for (const t of r.target) bad.push(`admin ${what}: target: ${t}`);
    };
    await look('characters');
    const first = page.locator('#main a, #main [role=button], #main li, #main .item').first();
    if (await first.count()) { await first.click().catch(() => {}); await page.waitForTimeout(800); await look('character card'); }
    await page.click('button[data-tab=accounts]').catch(() => {}); await page.waitForTimeout(600);
    const acc = page.locator('#main .item').first();
    if (await acc.count()) { await acc.click().catch(() => {}); await page.waitForTimeout(800); await look('account card'); }
    for (const tab of ['accounts', 'campaigns', 'history', 'characters']) {
      await page.click(`button[data-tab=${tab}]`).catch(() => {}); await page.waitForTimeout(600); await look(tab);
    }
    n++;
  }
  await browser.close();
  console.log(`${n} pages at 320 px`);
  if (bad.length) { console.log(bad.join('\n')); console.log(`${bad.length} problem(s)`); process.exit(1); }
  console.log('no problems');
})();
