// NSM — 1:1 fivem.net & NSM.exe (cfx-ui) Live Server Browser Engine
(function () {
  'use strict';

  function esc(s) {
    return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }

  // Render FiveM / Quake ^0-^9 color codes into <span class="cfx-cX"> segments
  function renderCfxTitle(raw, fallback) {
    var str = String(raw || fallback || 'NSM Dedicated Server');
    var parts = str.split(/(\^[0-9])/g);
    var curClass = '';
    var html = '';
    for (var i = 0; i < parts.length; i++) {
      var tok = parts[i];
      if (/^\^[0-9]$/.test(tok)) {
        curClass = 'cfx-c' + tok.charAt(1);
      } else if (tok) {
        if (curClass) {
          html += '<span class="' + curClass + '">' + esc(tok) + '</span>';
        } else {
          html += esc(tok);
        }
      }
    }
    return html || esc(fallback || 'NSM Dedicated Server');
  }

  function countryFlagEmoji(cc) {
    var code = String(cc || 'US').toUpperCase().trim();
    if (code.length !== 2) return '🌐';
    var A = 0x1f1e6;
    var c1 = code.charCodeAt(0) - 65;
    var c2 = code.charCodeAt(1) - 65;
    if (c1 < 0 || c1 > 25 || c2 < 0 || c2 > 25) return '🌐';
    return String.fromCodePoint(A + c1, A + c2);
  }

  function readSet(key) {
    try {
      var arr = JSON.parse(localStorage.getItem(key) || '[]');
      return Array.isArray(arr) ? arr : [];
    } catch (e) {
      return [];
    }
  }

  function writeSet(key, arr) {
    try {
      localStorage.setItem(key, JSON.stringify(arr));
    } catch (e) {}
  }

  // ---- NSM.exe Server Browser Controller ----
  var launcherRoot = document.getElementById('nsm-launcher');
  if (launcherRoot) {
    var state = {
      servers: Array.isArray(window.__NSM_INITIAL_SERVERS__) ? window.__NSM_INITIAL_SERVERS__ : [],
      tab: 'all', // 'all' | 'supporters' | 'favorites' | 'history'
      search: '',
      sortBy: 'players', // 'players' | 'upvotes' | 'name'
      hideEmpty: false,
      hideFull: false,
      tagFilters: {}, // tag -> true (inclusive) | false (exclusive)
      selectedId: null,
      favorites: readSet('nsm_fav_servers'),
      history: readSet('nsm_hist_servers')
    };

    var listEl = document.getElementById('nsm-srv-list');
    var detailsEl = document.getElementById('nsm-srv-details');
    var searchInput = document.getElementById('nsm-search-input');
    var searchClear = document.getElementById('nsm-search-clear');
    var sortSelect = document.getElementById('nsm-sort-select');
    var filterBtn = document.getElementById('nsm-filter-toggle');
    var filterDrawer = document.getElementById('nsm-filter-drawer');
    var hideEmptyChk = document.getElementById('nsm-hide-empty');
    var hideFullChk = document.getElementById('nsm-hide-full');
    var faucetContainer = document.getElementById('nsm-tag-faucets');
    var dcInput = document.getElementById('nsm-dc-input');
    var dcBtn = document.getElementById('nsm-dc-btn');

    function isFav(id) {
      return state.favorites.indexOf(id) !== -1;
    }
    function toggleFav(id) {
      var idx = state.favorites.indexOf(id);
      if (idx === -1) state.favorites.unshift(id);
      else state.favorites.splice(idx, 1);
      writeSet('nsm_fav_servers', state.favorites);
      renderAll();
    }
    function recordHistory(id) {
      if (!id) return;
      var idx = state.history.indexOf(id);
      if (idx !== -1) state.history.splice(idx, 1);
      state.history.unshift(id);
      if (state.history.length > 50) state.history.length = 50;
      writeSet('nsm_hist_servers', state.history);
      updateTabCounts();
    }

    function connectToServer(ep) {
      if (!ep) return;
      recordHistory(ep);
      window.location.href = 'fivem://connect/' + encodeURIComponent(ep);
    }

    function boostServer(ep, btnEl) {
      if (!ep) return;
      if (btnEl) btnEl.disabled = true;
      fetch('/api/servers/' + encodeURIComponent(ep) + '/upvote', {
        method: 'POST',
        headers: { 'Accept': 'application/json' }
      })
        .then(function (r) { return r.json(); })
        .then(function (d) {
          if (d && typeof d.upvotes === 'number') {
            for (var i = 0; i < state.servers.length; i++) {
              if ((state.servers[i].id || state.servers[i].EndPoint) === ep) {
                state.servers[i].upvotes = d.upvotes;
                state.servers[i].Upvotes = d.upvotes;
              }
            }
            renderAll();
          } else if (d && d.error === 'login required') {
            window.location.href = '/login?next=' + encodeURIComponent(window.location.pathname);
          }
        })
        .catch(function () {})
        .finally(function () {
          if (btnEl) btnEl.disabled = false;
        });
    }

    function normalizeServer(s) {
      var id = s.id || s.EndPoint || '';
      var rawName = s.rawName || s.Hostname || s.name || s.CleanName || id;
      var cleanName = s.name || s.CleanName || rawName.replace(/\^[0-9]/g, '').trim() || 'NSM Dedicated Server';
      var players = Number(s.players != null ? s.players : s.Clients) || 0;
      var maxPlayers = Number(s.maxPlayers != null ? s.maxPlayers : s.MaxClients) || 32;
      var upvotes = Number(s.upvotes != null ? s.upvotes : s.Upvotes) || 0;
      var burstPower = Number(s.burstPower != null ? s.burstPower : s.BurstPower) || 0;
      var tags = Array.isArray(s.tags) ? s.tags : (Array.isArray(s.Tags) ? s.Tags : []);
      return {
        id: id,
        rawName: rawName,
        name: cleanName,
        initial: (cleanName.charAt(0) || 'S').toUpperCase(),
        desc: s.desc || s.Desc || s.projectDescription || '',
        players: players,
        maxPlayers: maxPlayers,
        mode: s.mode || s.GameType || '',
        map: s.map || s.MapName || '',
        upvotes: upvotes,
        burstPower: burstPower,
        tags: tags,
        locale: s.locale || s.Locale || 'en-US',
        country: s.country || s.Country || 'US',
        onesync: s.onesync || s.OneSync || '',
        gameBuild: s.gameBuild || s.GameBuild || '',
        pureLevel: s.pureLevel || s.PureLevel || '',
        scriptHook: s.scriptHook || s.ScriptHook || '',
        bannerDetail: s.bannerDetail || s.BannerDetail || '',
        iconVersion: s.iconVersion || s.IconVersion || 0,
        resourcesCount: Number(s.resourcesCount != null ? s.resourcesCount : s.Resources) || 0,
        resources: Array.isArray(s.resources) ? s.resources : (Array.isArray(s.ResourceList) ? s.ResourceList : []),
        playersList: Array.isArray(s.playersList) ? s.playersList : (Array.isArray(s.PlayersList) ? s.PlayersList : []),
        serverVersion: s.serverVersion || s.ServerVersion || ''
      };
    }

    function getFilteredServers() {
      var list = state.servers.map(normalizeServer);

      // Tab filter
      if (state.tab === 'supporters') {
        list = list.filter(function (s) { return s.upvotes > 0 || s.burstPower > 0; });
      } else if (state.tab === 'favorites') {
        list = list.filter(function (s) { return isFav(s.id); });
      } else if (state.tab === 'history') {
        list = list.filter(function (s) { return state.history.indexOf(s.id) !== -1; });
      }

      // Hide Empty / Hide Full
      if (state.hideEmpty) {
        list = list.filter(function (s) { return s.players > 0; });
      }
      if (state.hideFull) {
        list = list.filter(function (s) { return s.players < s.maxPlayers; });
      }

      // Tag Faucets
      var tagKeys = Object.keys(state.tagFilters);
      for (var t = 0; t < tagKeys.length; t++) {
        var tg = tagKeys[t];
        var mode = state.tagFilters[tg];
        if (mode === true) {
          list = list.filter(function (s) {
            return s.tags.some(function (x) { return x.toLowerCase() === tg.toLowerCase(); });
          });
        } else if (mode === false) {
          list = list.filter(function (s) {
            return !s.tags.some(function (x) { return x.toLowerCase() === tg.toLowerCase(); });
          });
        }
      }

      // Search input (supports plain terms, ~tag, ~-tag, tag:xyz, locale:xyz)
      var q = state.search.trim();
      if (q) {
        var tokens = q.split(/\s+/);
        list = list.filter(function (s) {
          var hay = (s.name + ' ' + s.desc + ' ' + s.mode + ' ' + s.map + ' ' + s.id + ' ' + s.tags.join(' ')).toLowerCase();
          for (var i = 0; i < tokens.length; i++) {
            var tk = tokens[i];
            if (!tk) continue;
            if (tk.indexOf('~-') === 0 && tk.length > 2) {
              var negTag = tk.slice(2).toLowerCase();
              if (s.tags.some(function (x) { return x.toLowerCase().indexOf(negTag) !== -1; })) return false;
            } else if (tk.indexOf('~') === 0 && tk.length > 1) {
              var posTag = tk.slice(1).toLowerCase();
              if (!s.tags.some(function (x) { return x.toLowerCase().indexOf(posTag) !== -1; })) return false;
            } else if (tk.toLowerCase().indexOf('tag:') === 0) {
              var tVal = tk.slice(4).toLowerCase();
              if (tVal && !s.tags.some(function (x) { return x.toLowerCase().indexOf(tVal) !== -1; })) return false;
            } else if (tk.toLowerCase().indexOf('locale:') === 0) {
              var lVal = tk.slice(7).toLowerCase();
              if (lVal && s.locale.toLowerCase().indexOf(lVal) === -1) return false;
            } else if (hay.indexOf(tk.toLowerCase()) === -1) {
              return false;
            }
          }
          return true;
        });
      }

      // Sort
      list.sort(function (a, b) {
        if (state.sortBy === 'upvotes') {
          if (b.upvotes !== a.upvotes) return b.upvotes - a.upvotes;
          if (b.players !== a.players) return b.players - a.players;
          return a.name.localeCompare(b.name);
        }
        if (state.sortBy === 'name') {
          return a.name.localeCompare(b.name);
        }
        // default: players desc, then upvotes desc
        if (b.players !== a.players) return b.players - a.players;
        if (b.upvotes !== a.upvotes) return b.upvotes - a.upvotes;
        return a.name.localeCompare(b.name);
      });

      return list;
    }

    function updateTabCounts() {
      var allCnt = document.getElementById('nsm-cnt-all');
      var supCnt = document.getElementById('nsm-cnt-supporters');
      var favCnt = document.getElementById('nsm-cnt-favorites');
      var histCnt = document.getElementById('nsm-cnt-history');
      var normalized = state.servers.map(normalizeServer);
      if (allCnt) allCnt.textContent = normalized.length;
      if (supCnt) {
        supCnt.textContent = normalized.filter(function (s) { return s.upvotes > 0 || s.burstPower > 0; }).length;
      }
      if (favCnt) {
        favCnt.textContent = normalized.filter(function (s) { return isFav(s.id); }).length;
      }
      if (histCnt) {
        histCnt.textContent = normalized.filter(function (s) { return state.history.indexOf(s.id) !== -1; }).length;
      }
    }

    function renderTagFaucets() {
      if (!faucetContainer) return;
      var counts = {};
      state.servers.forEach(function (raw) {
        var s = normalizeServer(raw);
        s.tags.forEach(function (t) {
          counts[t] = (counts[t] || 0) + 1;
        });
      });
      var tags = Object.keys(counts).sort(function (a, b) {
        return counts[b] - counts[a] || a.localeCompare(b);
      }).slice(0, 24);

      if (!tags.length) {
        faucetContainer.innerHTML = '<span class="muted" style="font-size:12px">Tags appear automatically when servers announce sv_tags</span>';
        return;
      }

      faucetContainer.innerHTML = tags.map(function (t) {
        var st = state.tagFilters[t];
        var cls = 'nsm-faucet' + (st === true ? ' inclusive' : (st === false ? ' exclusive' : ''));
        return '<button type="button" class="' + cls + '" data-faucet-tag="' + esc(t) + '">' +
          '<span>' + esc(t) + '</span>' +
          '<span style="opacity:.55;font-size:11px">' + counts[t] + '</span>' +
        '</button>';
      }).join('');
    }

    function renderServerList(filtered) {
      if (!listEl) return;
      if (!filtered.length) {
        listEl.innerHTML =
          '<div style="padding:3rem 1.5rem;text-align:center">' +
            '<div style="font-size:1.15rem;font-weight:700;margin-bottom:6px;color:#fff">No matching servers found</div>' +
            '<p class="muted" style="font-size:0.88rem;max-width:440px;margin:0 auto 14px">' +
              (state.servers.length === 0
                ? 'No FXServer / NSM server has sent a heartbeat yet. Issue a key in Keymaster and start your server to see it live here and inside NSM.exe!'
                : 'Try clearing your active search query or tag filters.') +
            '</p>' +
            '<div style="display:flex;gap:8px;justify-content:center">' +
              '<a href="/keymaster" class="btn btn-primary btn-sm">Open Keymaster</a>' +
              '<a href="/docs#server-setup" class="btn btn-ghost btn-sm">Server Setup Guide</a>' +
            '</div>' +
          '</div>';
        return;
      }

      // Ensure a server is selected for the right-hand details inspector
      if (!state.selectedId || !filtered.some(function (s) { return s.id === state.selectedId; })) {
        state.selectedId = filtered[0].id;
      }

      listEl.innerHTML = filtered.map(function (s) {
        var selected = s.id === state.selectedId;
        var boosted = s.upvotes > 0 || s.burstPower > 0;
        var fav = isFav(s.id);
        var pct = s.maxPlayers > 0 ? Math.min(100, Math.round((s.players / s.maxPlayers) * 100)) : 0;
        var visibleTags = s.tags.slice(0, 4);
        var iconUrl = '/api/servers/icon/' + encodeURIComponent(s.id) + '.png';

        return (
          '<div class="nsm-srv-row' + (selected ? ' selected' : '') + (boosted ? ' boosted' : '') + '" data-srv-id="' + esc(s.id) + '">' +
            '<div class="nsm-srv-icon">' +
              '<img src="' + iconUrl + '" alt="" loading="lazy" onerror="this.style.display=\'none\';this.nextElementSibling.style.display=\'flex\'">' +
              '<span style="display:none;width:100%;height:100%;align-items:center;justify-content:center">' + esc(s.initial) + '</span>' +
            '</div>' +

            '<div class="nsm-srv-title-col">' +
              '<div class="nsm-srv-title-line">' +
                (boosted ? '<span class="nsm-premium-badge" title="Supporter / Boosted Server">Pt</span>' : '') +
                '<span>' + renderCfxTitle(s.rawName, s.name) + '</span>' +
              '</div>' +
              (s.desc ? '<div class="nsm-srv-desc">' + renderCfxTitle(s.desc, s.desc) + '</div>' : '<div class="nsm-srv-desc">' + esc(s.mode || 'GTA5') + (s.map ? ' • ' + esc(s.map) : '') + '</div>') +
            '</div>' +

            '<div class="nsm-srv-tags hide-on-hover">' +
              visibleTags.map(function (t) {
                return '<span class="nsm-tag-loaf" data-click-tag="' + esc(t) + '">' + esc(t) + '</span>';
              }).join('') +
            '</div>' +

            '<div class="nsm-srv-actions show-on-hover">' +
              '<button type="button" class="nsm-row-btn boost" data-boost-id="' + esc(s.id) + '" title="Boost™ Server Power">⚡ BOOST™ (' + s.upvotes + ')</button>' +
              '<button type="button" class="nsm-row-btn connect" data-connect-id="' + esc(s.id) + '">▶ CONNECT</button>' +
            '</div>' +

            '<div class="nsm-srv-decorator">' +
              '<span class="nsm-power-pill" title="Server Boost Power">⚡ ' + s.upvotes + '</span>' +
              '<span class="nsm-country-pill" title="Locale: ' + esc(s.locale) + '">' + countryFlagEmoji(s.country) + ' ' + esc(s.country) + '</span>' +
              '<button type="button" class="nsm-fav-btn' + (fav ? ' active' : '') + '" data-fav-id="' + esc(s.id) + '" title="Toggle Favorite">' + (fav ? '★' : '☆') + '</button>' +
            '</div>' +

            '<div class="nsm-srv-players">' +
              '<div class="nsm-srv-players-num">' + s.players + ' <span class="max">/ ' + s.maxPlayers + '</span></div>' +
              '<div class="nsm-srv-bar"><span style="width:' + pct + '%"></span></div>' +
            '</div>' +
          '</div>'
        );
      }).join('');
    }

    function renderServerDetails(filtered) {
      if (!detailsEl) return;
      var s = null;
      for (var i = 0; i < filtered.length; i++) {
        if (filtered[i].id === state.selectedId) {
          s = filtered[i];
          break;
        }
      }
      if (!s && state.servers.length) {
        s = normalizeServer(state.servers[0]);
      }
      if (!s) {
        detailsEl.innerHTML =
          '<div style="padding:2.5rem 1.5rem;text-align:center;color:var(--muted)">' +
            '<div style="font-size:1rem;font-weight:700;color:#fff;margin-bottom:6px">Server Inspector</div>' +
            '<p style="font-size:0.85rem">Select any server row on the left to inspect its live players, resources, tags, and connection endpoint.</p>' +
          '</div>';
        return;
      }

      var fav = isFav(s.id);
      var iconUrl = '/api/servers/icon/' + encodeURIComponent(s.id) + '.png';
      var bannerStyle = s.bannerDetail
        ? 'background-image:linear-gradient(to bottom, rgba(22,27,38,0.15), #161b26), url(\'' + esc(s.bannerDetail) + '\')'
        : '';

      var resChips = s.resources.slice(0, 40).map(function (r) {
        return '<span class="nsm-tag-loaf" style="font-family:var(--mono);font-size:11px">' + esc(r) + '</span>';
      }).join('');

      var tagChips = s.tags.map(function (t) {
        return '<span class="nsm-tag-loaf" data-click-tag="' + esc(t) + '" style="cursor:pointer">' + esc(t) + '</span>';
      }).join('');

      var playersHtml = '';
      if (s.playersList && s.playersList.length) {
        playersHtml = s.playersList.slice(0, 25).map(function (p) {
          return '<div style="display:flex;justify-content:space-between;padding:4px 0;border-bottom:1px solid rgba(229,238,255,0.06);font-size:12px">' +
            '<span>' + esc(p.name || 'Player') + '</span>' +
            '<span class="muted" style="font-family:var(--mono)">' + Number(p.ping || 0) + ' ms</span>' +
          '</div>';
        }).join('');
      } else {
        playersHtml = '<div class="muted" style="font-size:12px">No active players connected right now.</div>';
      }

      detailsEl.innerHTML =
        '<div class="nsm-details-banner" style="' + bannerStyle + '"></div>' +
        '<div class="nsm-details-body">' +
          '<div class="nsm-details-top">' +
            '<div class="nsm-details-icon">' +
              '<img src="' + iconUrl + '" alt="" onerror="this.style.display=\'none\';this.nextElementSibling.style.display=\'flex\'">' +
              '<span style="display:none;width:100%;height:100%;align-items:center;justify-content:center">' + esc(s.initial) + '</span>' +
            '</div>' +
            '<div style="flex:1;min-width:0">' +
              '<div class="nsm-details-title">' + renderCfxTitle(s.rawName, s.name) + '</div>' +
              '<div style="display:flex;align-items:center;gap:8px;margin-top:4px;font-size:12px;color:var(--muted)">' +
                '<span>👥 <strong style="color:#fff">' + s.players + '</strong>/' + s.maxPlayers + '</span>' +
                '<span>•</span>' +
                '<span style="color:var(--amber)">⚡ ' + s.upvotes + ' Boosts</span>' +
                '<span>•</span>' +
                '<span>' + countryFlagEmoji(s.country) + ' ' + esc(s.locale) + '</span>' +
              '</div>' +
            '</div>' +
          '</div>' +

          (s.desc ? '<div class="nsm-details-desc">' + renderCfxTitle(s.desc, s.desc) + '</div>' : '') +

          '<div class="nsm-details-cta">' +
            '<button type="button" class="nsm-connect-big" data-connect-id="' + esc(s.id) + '">▶ CONNECT IN NSM.EXE</button>' +
            '<button type="button" class="nsm-ctrl-btn" data-boost-id="' + esc(s.id) + '" style="border-color:rgba(255,174,0,0.45);color:#ffae00">⚡ Boost (' + s.upvotes + ')</button>' +
            '<button type="button" class="nsm-ctrl-btn" data-fav-id="' + esc(s.id) + '">' + (fav ? '★ Saved' : '☆ Favorite') + '</button>' +
            '<a href="/servers/' + encodeURIComponent(s.id) + '" class="nsm-ctrl-btn" title="Open standalone server page">↗</a>' +
          '</div>' +

          '<div style="display:flex;align-items:center;justify-content:space-between;gap:8px;padding:7px 10px;background:#11151e;border:1px solid rgba(229,238,255,0.10);border-radius:6px;font-family:var(--mono);font-size:12px">' +
            '<span style="color:var(--cyan);overflow:hidden;text-overflow:ellipsis;white-space:nowrap">connect ' + esc(s.id) + '</span>' +
            '<button type="button" class="nsm-row-btn" data-copy-cmd="connect ' + esc(s.id) + '" style="height:24px;font-size:11px">Copy F8</button>' +
          '</div>' +

          '<div class="nsm-loafs-grid">' +
            (s.mode ? '<span class="nsm-core-loaf">🎮 Mode: <strong>' + esc(s.mode) + '</strong></span>' : '') +
            (s.map ? '<span class="nsm-core-loaf">🗺️ Map: <strong>' + esc(s.map) + '</strong></span>' : '') +
            (s.onesync ? '<span class="nsm-core-loaf">🔄 OneSync: <strong>' + esc(s.onesync) + '</strong></span>' : '') +
            (s.gameBuild ? '<span class="nsm-core-loaf">📦 Build: <strong>' + esc(s.gameBuild) + '</strong></span>' : '') +
            (s.pureLevel ? '<span class="nsm-core-loaf">🛡️ Pure: <strong>L' + esc(s.pureLevel) + '</strong></span>' : '') +
            '<span class="nsm-core-loaf">🧩 Resources: <strong>' + s.resourcesCount + '</strong></span>' +
          '</div>' +

          (s.tags.length ? (
            '<div class="nsm-side-section">' +
              '<h4><span>Tags</span><span>' + s.tags.length + '</span></h4>' +
              '<div class="nsm-chip-cloud">' + tagChips + '</div>' +
            '</div>'
          ) : '') +

          '<div class="nsm-side-section">' +
            '<h4><span>Online Players</span><span>' + s.players + ' / ' + s.maxPlayers + '</span></h4>' +
            '<div style="max-height:140px;overflow-y:auto">' + playersHtml + '</div>' +
          '</div>' +

          (s.resources.length ? (
            '<div class="nsm-side-section">' +
              '<h4><span>Loaded Resources</span><span>' + s.resourcesCount + '</span></h4>' +
              '<div class="nsm-chip-cloud">' + resChips + '</div>' +
            '</div>'
          ) : '') +
        '</div>';
    }

    function renderAll() {
      updateTabCounts();
      renderTagFaucets();
      var filtered = getFilteredServers();
      renderServerList(filtered);
      renderServerDetails(filtered);
    }

    // Event bindings for tabs
    var tabBtns = launcherRoot.querySelectorAll('[data-nsm-tab]');
    for (var i = 0; i < tabBtns.length; i++) {
      tabBtns[i].addEventListener('click', function () {
        state.tab = this.getAttribute('data-nsm-tab') || 'all';
        for (var j = 0; j < tabBtns.length; j++) {
          tabBtns[j].classList.toggle('active', tabBtns[j] === this);
        }
        renderAll();
      });
    }

    if (searchInput) {
      searchInput.addEventListener('input', function () {
        state.search = searchInput.value || '';
        if (searchClear) searchClear.classList.toggle('visible', state.search.length > 0);
        renderAll();
      });
    }
    if (searchClear && searchInput) {
      searchClear.addEventListener('click', function () {
        searchInput.value = '';
        state.search = '';
        searchClear.classList.remove('visible');
        renderAll();
      });
    }
    if (sortSelect) {
      sortSelect.addEventListener('change', function () {
        state.sortBy = sortSelect.value || 'players';
        renderAll();
      });
    }
    if (filterBtn && filterDrawer) {
      filterBtn.addEventListener('click', function () {
        var open = filterDrawer.classList.toggle('open');
        filterBtn.classList.toggle('active', open);
      });
    }
    if (hideEmptyChk) {
      hideEmptyChk.addEventListener('change', function () {
        state.hideEmpty = !!hideEmptyChk.checked;
        renderAll();
      });
    }
    if (hideFullChk) {
      hideFullChk.addEventListener('change', function () {
        state.hideFull = !!hideFullChk.checked;
        renderAll();
      });
    }
    if (dcBtn && dcInput) {
      dcBtn.addEventListener('click', function () {
        var ep = dcInput.value.trim();
        if (ep) connectToServer(ep);
      });
      dcInput.addEventListener('keydown', function (ev) {
        if (ev.key === 'Enter') {
          ev.preventDefault();
          var ep = dcInput.value.trim();
          if (ep) connectToServer(ep);
        }
      });
    }

    // Delegated clicks inside launcherRoot
    launcherRoot.addEventListener('click', function (ev) {
      var target = ev.target;
      if (!target || !target.closest) return;

      var faucetBtn = target.closest('[data-faucet-tag]');
      if (faucetBtn) {
        var tg = faucetBtn.getAttribute('data-faucet-tag');
        var cur = state.tagFilters[tg];
        if (cur === undefined) state.tagFilters[tg] = true;
        else if (cur === true) state.tagFilters[tg] = false;
        else delete state.tagFilters[tg];
        renderAll();
        return;
      }

      var clickTag = target.closest('[data-click-tag]');
      if (clickTag) {
        ev.stopPropagation();
        var tagVal = clickTag.getAttribute('data-click-tag');
        if (searchInput && tagVal) {
          searchInput.value = 'tag:' + tagVal;
          state.search = searchInput.value;
          if (searchClear) searchClear.classList.add('visible');
          renderAll();
        }
        return;
      }

      var favBtn = target.closest('[data-fav-id]');
      if (favBtn) {
        ev.stopPropagation();
        toggleFav(favBtn.getAttribute('data-fav-id'));
        return;
      }

      var boostBtn = target.closest('[data-boost-id]');
      if (boostBtn) {
        ev.stopPropagation();
        boostServer(boostBtn.getAttribute('data-boost-id'), boostBtn);
        return;
      }

      var connBtn = target.closest('[data-connect-id]');
      if (connBtn) {
        ev.stopPropagation();
        connectToServer(connBtn.getAttribute('data-connect-id'));
        return;
      }

      var copyBtn = target.closest('[data-copy-cmd]');
      if (copyBtn) {
        ev.stopPropagation();
        var cmd = copyBtn.getAttribute('data-copy-cmd');
        if (navigator.clipboard && cmd) {
          navigator.clipboard.writeText(cmd);
          copyBtn.textContent = 'Copied!';
          setTimeout(function () { copyBtn.textContent = 'Copy F8'; }, 1200);
        }
        return;
      }

      var row = target.closest('[data-srv-id]');
      if (row) {
        var id = row.getAttribute('data-srv-id');
        state.selectedId = id;
        recordHistory(id);
        renderAll();
      }
    });

    // Initial render
    renderAll();

    // Live polling from /api/servers/live every 5 seconds (same source as NSM.exe)
    function pollLiveServers() {
      fetch('/api/servers/live')
        .then(function (r) { return r.json(); })
        .then(function (d) {
          if (!d || !Array.isArray(d.servers)) return;
          state.servers = d.servers;
          renderAll();
          var pEls = document.querySelectorAll('[data-stat="players"]');
          var sEls = document.querySelectorAll('[data-stat="servers"]');
          for (var i = 0; i < pEls.length; i++) pEls[i].textContent = d.players;
          for (var j = 0; j < sEls.length; j++) sEls[j].textContent = d.count;
        })
        .catch(function () {});
    }
    setInterval(pollLiveServers, 5000);
  } else {
    // Poll live player/server counts on pages without the server launcher
    setInterval(function () {
      fetch('/api/servers/live')
        .then(function (r) { return r.json(); })
        .then(function (d) {
          if (!d) return;
          var pEls = document.querySelectorAll('[data-stat="players"]');
          var sEls = document.querySelectorAll('[data-stat="servers"]');
          for (var i = 0; i < pEls.length; i++) pEls[i].textContent = d.players;
          for (var j = 0; j < sEls.length; j++) sEls[j].textContent = d.count;
        })
        .catch(function () {});
    }, 8000);
  }
})();
