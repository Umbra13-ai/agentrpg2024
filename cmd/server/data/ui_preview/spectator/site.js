(() => {
  const dialog = document.getElementById('appearance');
  const options = [...dialog.querySelectorAll('input[name="site-theme"]')];
  const current = options.find(option => option.value === document.documentElement.dataset.theme);
  const selected = current || options.find(option => option.value === 'dark');
  selected.checked = true;
  document.documentElement.dataset.theme = selected.value;

  dialog.addEventListener('change', event => {
    if (!options.includes(event.target)) return;
    const theme = event.target.value;
    document.documentElement.dataset.theme = theme;
    try { localStorage.setItem('theme', theme); } catch (_) {}
  });

  document.querySelectorAll('[data-open-appearance]').forEach(button => {
    button.addEventListener('click', () => dialog.showModal());
  });
})();
