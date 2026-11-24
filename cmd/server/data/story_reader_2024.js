(function () {
  // The Campaign Story Reader (the owner's mock-up, data/ui_preview/story-reader-2026-11-23/index.html). The story is window.__STORY__ (story_reader_2024.go);
  // the reader keeps its place, bookmarks and comfort settings in this browser, one set per campaign.
  var STORY = window.__STORY__ || { chapters: [] };
  var STORY_CHAPTERS = STORY.chapters || [];
  var STORAGE_KEY = 'agentRpg_reader_' + STORY.campaign_id + '_v1';
  var ALL = []; // every passage in reading order: {id, chapterId, chapterIndex}
  STORY_CHAPTERS.forEach(function (ch, ci) { ch.entries.forEach(function (e) { ALL.push({ id: e.id, chapterId: ch.id, chapterIndex: ci }); }); });
  var POS = {}; ALL.forEach(function (a, i) { POS[a.id] = i; });

  function esc(s) { return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) { return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]; }); }
  function $(id) { return document.getElementById(id); }
  var SIZES = ['sm', 'md', 'lg'];
  // in the Live Table (STORY.inline) the scroll mode scrolls its own box, not the whole page
  var INLINE = Boolean(STORY.inline);
  function scrollBox() { return INLINE ? $('scroll-container') : null; }
  function scrollMax() { var b = scrollBox(); return b ? b.scrollHeight - b.clientHeight : document.documentElement.scrollHeight - window.innerHeight; }
  function scrollPos() { var b = scrollBox(); return b ? b.scrollTop : window.scrollY; }
  function scrollGo(top) { var b = scrollBox(); if (b) b.scrollTo({ top: top, behavior: 'smooth' }); else window.scrollTo({ top: top, behavior: 'smooth' }); }
  function loadSavedState() {
    try { var raw = localStorage.getItem(STORAGE_KEY); return raw ? JSON.parse(raw) : null; } catch (e) { return null; }
  }
  var saved = loadSavedState();
  var READER = {
    mode: (saved && saved.mode) || 'flip',
    pageIndex: (saved && typeof saved.pageIndex === 'number') ? saved.pageIndex : 0,
    scrollRatio: (saved && typeof saved.scrollRatio === 'number') ? saved.scrollRatio : 0,
    fontSize: (saved && saved.fontSize) || 'md',
    showDice: (saved && typeof saved.showDice === 'boolean') ? saved.showDice : true,
    bookmarks: (saved && Array.isArray(saved.bookmarks)) ? saved.bookmarks : [],
    readUpTo: (saved && typeof saved.readUpTo === 'number') ? saved.readUpTo : -1, // the furthest passage read (its place in ALL)
    firstPassage: saved && typeof saved.firstPassage === 'string' ? saved.firstPassage : '', // the first passage of the page the reader was on
    hasPriorSession: Boolean(saved && (saved.pageIndex > 0 || saved.scrollRatio > 0.03 || saved.readUpTo >= 0)),
    pages: []
  };
  function saveState() {
    try {
      var p = READER.pages[READER.pageIndex];
      localStorage.setItem(STORAGE_KEY, JSON.stringify({
        mode: READER.mode, pageIndex: READER.pageIndex, scrollRatio: READER.scrollRatio, fontSize: READER.fontSize, showDice: READER.showDice,
        bookmarks: READER.bookmarks, readUpTo: READER.readUpTo, firstPassage: p && p.entries.length ? p.entries[0].id : '', lastVisitedAt: new Date().toISOString()
      }));
    } catch (e) {}
  }
  function noteRead(pos) { if (pos > READER.readUpTo) READER.readUpTo = pos; }

  // Paginate chapters into comfortable book pages (2-3 passages per page)
  function buildPages() {
    var pages = [], per = READER.fontSize === 'lg' ? 2 : 3;
    STORY_CHAPTERS.forEach(function (ch, chIdx) {
      for (var i = 0; i < ch.entries.length; i += per) {
        pages.push({ chapterId: ch.id, chapterIndex: chIdx, chapterNum: ch.num, chapterTitle: ch.title, location: ch.location, readTime: ch.read_time,
          isChapterStart: i === 0, entries: ch.entries.slice(i, i + per) });
      }
    });
    READER.pages = pages;
    if (READER.pageIndex >= pages.length) READER.pageIndex = pages.length - 1;
    if (READER.pageIndex < 0) READER.pageIndex = 0;
  }
  function pageOfEntry(id) {
    for (var i = 0; i < READER.pages.length; i++) { if (READER.pages[i].entries.some(function (e) { return e.id === id; })) return i; }
    return -1;
  }
  // the creatures a passage shows (story_creatures_2024.go): their tokens, larger at their first passage of a chapter; a tap enlarges one
  function renderCast(entry) {
    if (!entry.creatures || !entry.creatures.length) return '';
    return '<div class="story-cast">' + entry.creatures.map(function (c) {
      return '<button type="button" class="cast-token' + (c.first ? ' first' : '') + '" aria-label="' + esc(c.name) + ': show the token larger"><img src="' + esc(c.token) +
        '" alt="' + esc(c.name) + '" loading="lazy" /><span class="cast-name">' + esc(c.name) + '</span></button>';
    }).join('') + '</div>';
  }
  function renderEntryHtml(entry) {
    var isSaved = READER.bookmarks.indexOf(entry.id) >= 0;
    var star = '<button class="bookmark-star ' + (isSaved ? 'saved' : '') + '" data-bookmark="' + esc(entry.id) + '" aria-label="' + (isSaved ? 'Remove bookmark' : 'Bookmark this moment') + '" title="' + (isSaved ? 'Remove bookmark' : 'Bookmark this moment') + '">' + (isSaved ? '★' : '☆') + '</button>';
    var dice = entry.dice ? '<div class="dice-chip ' + esc(entry.tone || '') + '">🎲 ' + esc(entry.dice) + '</div>' : '';
    if (entry.type === 'gm') {
      return '<div class="story-entry entry-gm ' + (entry.opener ? 'entry-opener' : '') + '" id="entry-' + esc(entry.id) + '">' +
        '<div style="display:flex; justify-content:space-between; align-items:center; margin-bottom:0.2rem;">' +
        '<span class="font-mono" style="font-size:0.75rem; color:var(--text-muted); text-transform:uppercase; letter-spacing:0.06em;">' + esc(entry.speaker || 'Narrator') + '</span>' + star + '</div>' +
        renderCast(entry) + '<p class="story-prose font-serif-story">' + esc(entry.text) + '</p>' + dice + '</div>';
    }
    return '<div class="story-entry entry-pc" id="entry-' + esc(entry.id) + '"><div class="entry-speaker"><span class="speaker-name">' + esc(entry.icon || '⚔️') + ' ' + esc(entry.speaker) + '</span>' + star + '</div>' +
      '<p class="story-prose font-serif-story">' + esc(entry.text) + '</p>' + dice + '</div>';
  }
  function renderFlipPage(animDir) {
    var page = READER.pages[READER.pageIndex], total = READER.pages.length, el = $('book-page');
    el.classList.remove('flip-next', 'flip-prev');
    if (animDir === 'next') { void el.offsetWidth; el.classList.add('flip-next'); } else if (animDir === 'prev') { void el.offsetWidth; el.classList.add('flip-prev'); }
    var head = page.isChapterStart ? '<div style="margin-bottom:0.9rem;"><div class="chapter-kicker">' + esc(page.chapterNum) + '</div><h1 class="chapter-heading font-rpg">' + esc(page.chapterTitle) +
      '</h1><div class="scene-sub">' + esc(page.location) + ' · ' + esc(page.readTime) + '</div></div>' : '';
    el.innerHTML = '<div><div class="page-header"><span>' + esc(page.chapterNum) + ' — ' + esc(page.chapterTitle) + '</span><span class="font-mono">' + esc(page.location) + '</span></div>' +
      '<div class="story-body">' + head + page.entries.map(renderEntryHtml).join('') + '</div></div>' +
      '<div class="page-footer"><span>Tap ← / → or swipe to turn</span><span class="font-mono">Page ' + (READER.pageIndex + 1) + ' of ' + total + '</span></div>';
    ['', '-top'].forEach(function (s) { // the bar under the page and the same bar above it
      $('page-counter' + s).textContent = 'Page ' + (READER.pageIndex + 1) + ' / ' + total;
      var scrubber = $('page-scrubber' + s); scrubber.max = String(total - 1); scrubber.value = String(READER.pageIndex);
      $('btn-prev-page' + s).disabled = READER.pageIndex === 0;
      $('btn-next-page' + s).disabled = READER.pageIndex === total - 1;
    });
    $('topbar-subtitle').textContent = page.chapterNum + ' · ' + page.chapterTitle;
    if (page.entries.length) noteRead(POS[page.entries[page.entries.length - 1].id]);
    $('reader-progress-fill').style.width = Math.round(((READER.pageIndex + 1) / total) * 100) + '%';
    updateNewCount();
  }
  function renderScrollMode() {
    $('scroll-container').innerHTML = STORY_CHAPTERS.map(function (ch) {
      return '<article class="scroll-chapter-card" id="scroll-' + esc(ch.id) + '"><div class="chapter-kicker">' + esc(ch.num) + '</div><h2 class="chapter-heading font-rpg">' + esc(ch.title) +
        '</h2><div class="scene-sub">' + esc(ch.location) + ' · ' + esc(ch.read_time) + '</div>' + ch.entries.map(renderEntryHtml).join('') + '</article>';
    }).join('');
  }
  function renderToc() {
    var cur = READER.pages[READER.pageIndex];
    $('toc-chapters').innerHTML = STORY_CHAPTERS.map(function (ch, idx) {
      var first = READER.pages.findIndex(function (p) { return p.chapterId === ch.id; });
      var isCurrent = cur && cur.chapterId === ch.id;
      var lastPos = POS[ch.entries[ch.entries.length - 1].id];
      var isRead = !isCurrent && READER.readUpTo >= lastPos;
      var label = isCurrent ? '● Reading now' : isRead ? '✓ Read' : 'Unread';
      var color = isCurrent ? 'var(--accent-gold)' : isRead ? 'var(--accent-emerald)' : 'var(--text-muted)';
      return '<div class="toc-item ' + (isCurrent ? 'is-current' : '') + '" data-jump-chapter="' + esc(ch.id) + '" data-jump-page="' + first + '">' +
        '<div style="display:flex; justify-content:space-between; align-items:center; gap:0.4rem;"><strong class="font-rpg" style="font-size:0.86rem; color:var(--text-primary);">' + esc(ch.num) + ': ' + esc(ch.title) +
        '</strong><span class="font-mono" style="font-size:0.75rem; color:' + color + ';">' + label + '</span></div>' +
        '<div style="font-size:0.75rem; color:var(--text-secondary); margin-top:0.2rem;">' + esc(ch.location) + ' · ' + esc(ch.read_time) + ' · Page ' + (first + 1) + '</div></div>';
    }).join('');
    var all = [];
    STORY_CHAPTERS.forEach(function (ch) { ch.entries.forEach(function (e) { all.push({ e: e, chapterNum: ch.num }); }); });
    var savedEntries = all.filter(function (x) { return READER.bookmarks.indexOf(x.e.id) >= 0; });
    $('toc-bookmarks').innerHTML = savedEntries.length ? savedEntries.map(function (x) {
      var idx = pageOfEntry(x.e.id);
      return '<div class="toc-item" data-jump-entry="' + esc(x.e.id) + '" data-jump-page="' + idx + '"><div style="display:flex; justify-content:space-between; font-size:0.75rem; color:var(--accent-gold); font-weight:700;"><span>★ ' +
        esc(x.e.speaker || 'Narrator') + ' (' + esc(x.chapterNum) + ')</span><span class="font-mono">Page ' + (idx + 1) + '</span></div><div style="font-size:0.75rem; color:var(--text-secondary); margin-top:0.2rem; display:-webkit-box; -webkit-line-clamp:2; -webkit-box-orient:vertical; overflow:hidden;">' +
        esc(x.e.text) + '</div></div>';
    }).join('') : '<div style="font-size:0.78rem; color:var(--text-muted); padding:0.4rem 0;">Tap ☆ on any passage to save a bookmark here.</div>';
  }
  function applyPreferences() {
    document.body.classList.remove('story-size-sm', 'story-size-md', 'story-size-lg');
    document.body.classList.add('story-size-' + READER.fontSize);
    document.body.classList.toggle('hide-dice', !READER.showDice);
    $('size-range').value = String(SIZES.indexOf(READER.fontSize));
    var diceBtn = $('btn-dice-toggle');
    diceBtn.classList.toggle('active', READER.showDice);
    diceBtn.textContent = READER.showDice ? 'Rolls shown' : 'Rolls hidden';
    diceBtn.setAttribute('aria-pressed', READER.showDice ? 'true' : 'false');
    $('btn-mode-flip').classList.toggle('active', READER.mode === 'flip');
    $('btn-mode-scroll').classList.toggle('active', READER.mode === 'scroll');
    $('flip-container').classList.toggle('hidden', READER.mode !== 'flip');
    $('scroll-container').classList.toggle('hidden', READER.mode !== 'scroll');
  }
  function goToPage(idx, animDir) {
    READER.pageIndex = Math.max(0, Math.min(READER.pages.length - 1, idx));
    renderFlipPage(animDir || '');
    saveState();
  }
  // how many passages come after the furthest one read (for a reader who has been here before): what has been played since
  function updateNewCount() {
    var n = ALL.length - 1 - READER.readUpTo, btn = $('btn-new');
    if (!btn) return;
    btn.classList.toggle('hidden', n <= 0 || !READER.hasPriorSession);
    btn.textContent = 'View unread';
  }
  function jumpToPassage(id) {
    if (READER.mode === 'flip') { var p = pageOfEntry(id); if (p >= 0) goToPage(p, p > READER.pageIndex ? 'next' : 'prev'); return; }
    var t = document.getElementById('entry-' + id); if (t) t.scrollIntoView({ behavior: 'smooth', block: 'start' });
  }

  // Event wiring
  ['', '-top'].forEach(function (s) {
    $('btn-prev-page' + s).addEventListener('click', function () { goToPage(READER.pageIndex - 1, 'prev'); });
    $('btn-next-page' + s).addEventListener('click', function () { goToPage(READER.pageIndex + 1, 'next'); });
    $('page-scrubber' + s).addEventListener('input', function (e) { var n = Number(e.target.value); goToPage(n, n > READER.pageIndex ? 'next' : 'prev'); });
  });
  document.addEventListener('keydown', function (e) {
    if (READER.mode !== 'flip' || /^(INPUT|TEXTAREA|SELECT)$/.test((e.target || {}).tagName || '')) return;
    if (e.key === 'ArrowRight') goToPage(READER.pageIndex + 1, 'next');
    if (e.key === 'ArrowLeft') goToPage(READER.pageIndex - 1, 'prev');
  });
  var touchStartX = 0, touchStartY = 0, stage = $('book-stage');
  stage.addEventListener('touchstart', function (e) { if (!e.touches.length) return; touchStartX = e.touches[0].clientX; touchStartY = e.touches[0].clientY; }, { passive: true });
  stage.addEventListener('touchend', function (e) {
    if (!e.changedTouches.length || READER.mode !== 'flip') return;
    var dx = e.changedTouches[0].clientX - touchStartX, dy = e.changedTouches[0].clientY - touchStartY;
    if (Math.abs(dx) > 48 && Math.abs(dx) > Math.abs(dy) * 1.2) { if (dx < 0) goToPage(READER.pageIndex + 1, 'next'); else goToPage(READER.pageIndex - 1, 'prev'); }
  }, { passive: true });
  $('btn-mode-flip').addEventListener('click', function () { READER.mode = 'flip'; applyPreferences(); renderFlipPage(); saveState(); });
  $('btn-mode-scroll').addEventListener('click', function () {
    READER.mode = 'scroll'; applyPreferences(); renderScrollMode(); saveState();
    setTimeout(function () {
      var docH = scrollMax();
      if (docH > 0 && READER.scrollRatio > 0) scrollGo(Math.round(docH * READER.scrollRatio));
    }, 40);
  });
  var scrollTick = null;
  (INLINE ? $('scroll-container') : window).addEventListener('scroll', function () {
    if (READER.mode !== 'scroll') return;
    clearTimeout(scrollTick);
    scrollTick = setTimeout(function () {
      var docH = scrollMax();
      if (docH > 0) {
        var ratio = Math.max(0, Math.min(1, scrollPos() / docH));
        READER.scrollRatio = ratio;
        noteRead(Math.min(ALL.length - 1, Math.round(ratio * (ALL.length - 1))));
        $('reader-progress-fill').style.width = Math.round(ratio * 100) + '%';
        updateNewCount(); saveState();
      }
    }, 120);
  }, { passive: true });
  $('btn-dice-toggle').addEventListener('click', function () { READER.showDice = !READER.showDice; applyPreferences(); saveState(); });
  // one Aa button opens a slider for the three text sizes
  var sizeBtn = $('btn-size'), sizePop = $('size-pop');
  sizeBtn.addEventListener('click', function (e) {
    e.stopPropagation();
    var open = sizePop.classList.toggle('hidden') === false;
    sizeBtn.setAttribute('aria-expanded', open ? 'true' : 'false'); sizeBtn.classList.toggle('active', open);
  });
  document.addEventListener('click', function (e) {
    if (!sizePop.contains(e.target) && e.target !== sizeBtn) { sizePop.classList.add('hidden'); sizeBtn.setAttribute('aria-expanded', 'false'); sizeBtn.classList.remove('active'); }
  });
  $('size-range').addEventListener('input', function () {
    var p = READER.pages[READER.pageIndex], keep = p && p.entries.length ? p.entries[0].id : '';
    READER.fontSize = SIZES[Number(this.value)] || 'md'; buildPages();
    var at = keep ? pageOfEntry(keep) : -1; if (at >= 0) READER.pageIndex = at; // the same passage stays in view
    applyPreferences(); renderFlipPage(); saveState();
  });
  document.addEventListener('click', function (e) {
    var cast = e.target.closest('.cast-token'); if (cast) { cast.classList.toggle('open'); return; }
    var star = e.target.closest('[data-bookmark]'); if (!star) return;
    var id = star.dataset.bookmark;
    if (READER.bookmarks.indexOf(id) >= 0) READER.bookmarks = READER.bookmarks.filter(function (b) { return b !== id; }); else READER.bookmarks.push(id);
    saveState();
    if (READER.mode === 'flip') renderFlipPage(); else renderScrollMode();
  });
  var toc = $('toc-backdrop');
  $('btn-toc').addEventListener('click', function () { renderToc(); toc.classList.remove('hidden'); });
  $('btn-close-toc').addEventListener('click', function () { toc.classList.add('hidden'); });
  toc.addEventListener('click', function (e) {
    if (e.target === toc) { toc.classList.add('hidden'); return; }
    var item = e.target.closest('[data-jump-page]'); if (!item) return;
    var pageIdx = Number(item.dataset.jumpPage), entryId = item.dataset.jumpEntry, chId = item.dataset.jumpChapter;
    toc.classList.add('hidden');
    if (READER.mode === 'flip') goToPage(pageIdx, pageIdx > READER.pageIndex ? 'next' : 'prev');
    else { var t = entryId ? $('entry-' + entryId) : $('scroll-' + chId); if (t) t.scrollIntoView({ behavior: 'smooth', block: 'start' }); }
  });
  $('btn-reset-progress').addEventListener('click', function () {
    READER.pageIndex = 0; READER.scrollRatio = 0; READER.readUpTo = -1; saveState(); toc.classList.add('hidden');
    if (READER.mode === 'flip') goToPage(0, 'prev'); else scrollGo(0);
  });
  $('btn-new').addEventListener('click', function () { var a = ALL[Math.min(ALL.length - 1, READER.readUpTo + 1)]; if (a) jumpToPassage(a.id); });
  $('btn-latest').addEventListener('click', function () {
    if (READER.mode === 'flip') goToPage(READER.pages.length - 1, 'next'); else scrollGo(scrollMax());
  });
  $('btn-refresh').addEventListener('click', function () { saveState(); location.reload(); });

  // the reader's bar sits under the site's own bar
  function placeBar() { var bar = document.querySelector('.topbar'); document.documentElement.style.setProperty('--story-top', (bar && !INLINE ? bar.offsetHeight : 0) + 'px'); }
  placeBar(); window.addEventListener('resize', placeBar);

  // Init
  if (!ALL.length) { $('story-empty').classList.remove('hidden'); $('story-reader').classList.add('hidden'); return; }
  buildPages();
  if (READER.firstPassage) { var at = pageOfEntry(READER.firstPassage); if (at >= 0) READER.pageIndex = at; }
  applyPreferences(); renderFlipPage(); renderScrollMode();
})();
