// NSM website interactive client script: server list search/tag/sort filter + live player/server counters.
(function () {
  var search = document.getElementById('srv-search');
  var sortSel = document.getElementById('srv-sort');
  var list = document.getElementById('srv-list');
  var tagBar = document.getElementById('srv-tag-filters');
  var activeTag = '';

  function filterAndSortServers() {
    if (!list) return;
    var q = search ? search.value.toLowerCase().trim() : '';
    var sortBy = sortSel ? sortSel.value : 'players';
    var rows = Array.prototype.slice.call(list.querySelectorAll('.srv-row'));

    rows.forEach(function (r) {
      var name = (r.getAttribute('data-name') || '').toLowerCase();
      var tags = (r.getAttribute('data-tags') || '').toLowerCase();
      var matchQ = !q || name.indexOf(q) !== -1 || tags.indexOf(q) !== -1;
      var matchTag = !activeTag || tags.indexOf(activeTag.toLowerCase()) !== -1;
      r.style.display = (matchQ && matchTag) ? '' : 'none';
    });

    rows.sort(function (a, b) {
      if (sortBy === 'upvotes') {
        return (parseInt(b.getAttribute('data-upvotes') || '0', 10)) - (parseInt(a.getAttribute('data-upvotes') || '0', 10));
      }
      if (sortBy === 'name') {
        return (a.getAttribute('data-name') || '').localeCompare(b.getAttribute('data-name') || '');
      }
      return (parseInt(b.getAttribute('data-players') || '0', 10)) - (parseInt(a.getAttribute('data-players') || '0', 10));
    });

    rows.forEach(function (r) {
      list.appendChild(r);
    });
  }

  if (search) search.addEventListener('input', filterAndSortServers);
  if (sortSel) sortSel.addEventListener('change', filterAndSortServers);
  if (tagBar) {
    tagBar.addEventListener('click', function (e) {
      var btn = e.target.closest('.tag-btn');
      if (!btn) return;
      Array.prototype.forEach.call(tagBar.querySelectorAll('.tag-btn'), function (b) {
        b.classList.remove('on');
      });
      btn.classList.add('on');
      activeTag = btn.getAttribute('data-tag') || '';
      filterAndSortServers();
    });
  }

  // Live player & server count refresh every 10s
  function refreshLiveCounts() {
    fetch('/runtime/counts.json')
      .then(function (r) { return r.json(); })
      .then(function (d) {
        if (!d) return;
        var pEls = ['stat-players', 'stat-players-pill', 'nav-players', 'srv-total-players'];
        var sEls = ['stat-servers', 'stat-servers-pill', 'srv-total-count'];
        pEls.forEach(function (id) {
          var el = document.getElementById(id);
          if (el && d.players !== undefined) el.textContent = d.players;
        });
        sEls.forEach(function (id) {
          var el = document.getElementById(id);
          if (el && d.servers !== undefined) el.textContent = d.servers;
        });
      })
      .catch(function () {});
  }

  setInterval(refreshLiveCounts, 10000);
})();
