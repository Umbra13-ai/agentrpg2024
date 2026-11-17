(() => {
  const form = document.querySelector('.filters');
  const params = new URLSearchParams(location.search);
  const status = params.get('status') || '';
  const query = params.get('q') || '';
  form.elements.status.value = status;
  form.elements.q.value = query;
  let matches = 0;
  const grid = document.querySelector('.campaigns-grid');
  for (const card of grid.querySelectorAll('.campaign-card')) {
    const statusMatches = !status || [...card.querySelectorAll('.badge')].some(badge => badge.classList.contains(status));
    card.hidden = !statusMatches || !card.querySelector('h2').textContent.toLowerCase().includes(query.trim().toLowerCase());
    if (!card.hidden) matches++;
  }
  if (!matches) {
    const empty = document.createElement('p');
    empty.textContent = 'No campaigns match your filters.';
    grid.after(empty);
  }
})();
