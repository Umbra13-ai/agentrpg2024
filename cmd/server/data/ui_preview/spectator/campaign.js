(() => {
  const key = 'spectator:' + location.pathname;
  let state = {};
  try { state = JSON.parse(sessionStorage.getItem(key)) || {}; } catch (_) {}

  function save() {
    try { sessionStorage.setItem(key, JSON.stringify(state)); } catch (_) {}
  }

  const sections = document.querySelector('.campaign-sections');
  const sectionButtons = [...sections.querySelectorAll('[data-panel]')];
  const narrow = matchMedia('(max-width: 47.99rem)');

  function showPanel(panel) {
    if (!sectionButtons.some(button => button.dataset.panel === panel)) panel = 'journal';
    state.panel = panel;
    const mobile = narrow.matches;
    sections.hidden = !mobile;
    for (const button of sectionButtons) {
      const selected = button.dataset.panel === panel;
      button.setAttribute('aria-pressed', String(selected));
      document.getElementById(button.getAttribute('aria-controls')).hidden = mobile && !selected;
    }
    document.querySelector('.campaign-sidebar').hidden = mobile && panel === 'journal';
    save();
  }
  for (const button of sectionButtons) {
    button.addEventListener('click', () => showPanel(button.dataset.panel));
  }
  narrow.addEventListener('change', () => showPanel(state.panel));
  showPanel(state.panel);

  const filter = document.querySelector('.journal-filter');
  const entries = [...document.querySelectorAll('.journal .feed-item')];
  const viewButtons = [...filter.querySelectorAll('[data-view]')];

  function showView(view) {
    const story = view !== 'all';
    state.view = story ? 'story' : 'all';
    for (const button of viewButtons) {
      button.setAttribute('aria-pressed', String(button.dataset.view === state.view));
    }
    for (const entry of entries) entry.hidden = story && entry.classList.contains('coordination');
    document.querySelector('.story-empty').hidden = !story || !entries.length || entries.some(entry => !entry.hidden);
    save();
  }
  for (const button of viewButtons) {
    button.addEventListener('click', () => showView(button.dataset.view));
  }
  filter.hidden = !entries.some(entry => entry.classList.contains('coordination'));
  showView(state.view);

  // Existing auto-refresh behavior, with session-local journal state.
  document.querySelectorAll('.campaign-sidebar details.fold').forEach(detail => {
    detail.open = Boolean(state[detail.id]);
    detail.addEventListener('toggle', () => {
      state[detail.id] = detail.open;
      save();
    });
  });
})();
