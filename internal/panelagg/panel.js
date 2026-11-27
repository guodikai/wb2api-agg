// panel.js 注入到 misakano 主面板的小扩展：增加「平台账号」页签。
//
// 为什么要注入而不是改上游 index.html：index.html 是上游的高频改动文件，
// 直接改会让每次同步都解冲突。这里由网关在 GET /panel/ 的响应里插一行
// <script src="/panel/agg/panel.js" defer>，上游文件一个字节不动。
(function () {
  'use strict';
  var LS_KEY = 'wb2api.key'; // 与主面板同一个 api_key
  var VIEW = 'platforms';

  function key() {
    try { return localStorage.getItem(LS_KEY) || ''; } catch (e) { return ''; }
  }

  function api(path) {
    var h = {};
    var k = key();
    if (k) h['Authorization'] = 'Bearer ' + k;
    return fetch(path, { headers: h }).then(function (r) {
      return r.json().then(function (j) {
        if (!r.ok) throw new Error((j.error && j.error.message) || ('HTTP ' + r.status));
        return j;
      });
    });
  }

  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text != null) e.textContent = text;
    return e;
  }

  function buildSection() {
    var sec = el('section', 'view');
    sec.id = 'view-' + VIEW;
    sec.hidden = true;

    var head = el('div', 'view-head');
    var h2 = el('h2', null, '平台账号');
    head.appendChild(h2);
    var link = el('a', 'btn', '登录 / 管理码道');
    link.href = '/panel/agg/';
    link.target = '_blank';
    link.style.marginLeft = 'auto';
    head.appendChild(link);
    sec.appendChild(head);

    var note = el('p', 'hint', 'WorkBuddy 账号在上面的「账号池」里；这里是本网关新增的平台（码道）。数据来自 /panel/agg/api/overview。');
    sec.appendChild(note);

    var box = el('div');
    box.id = 'aggPlatformBox';
    sec.appendChild(box);

    var h3s = el('h2', null, '签到状态');
    h3s.style.marginTop = '18px';
    sec.appendChild(h3s);
    var sbox = el('div');
    sbox.id = 'aggSigninBox';
    sbox.className = 'hint';
    sbox.textContent = '加载中…';
    sec.appendChild(sbox);

    var h3c = el('h2', null, '积分构成 / 余额趋势');
    h3c.style.marginTop = '18px';
    sec.appendChild(h3c);
    var cbox = el('div');
    cbox.id = 'aggCreditBox';
    cbox.className = 'hint';
    cbox.textContent = '加载中…';
    sec.appendChild(cbox);

    var h3 = el('h2', null, '最近请求');
    h3.style.marginTop = '18px';
    sec.appendChild(h3);
    var reqBox = el('div');
    reqBox.id = 'aggReqBox';
    reqBox.className = 'hint';
    reqBox.textContent = '加载中…';
    sec.appendChild(reqBox);
    return sec;
  }

  function render(data) {
    var box = document.getElementById('aggPlatformBox');
    if (!box) return;
    box.innerHTML = '';

    var table = el('table', 'table');
    var thead = el('thead');
    var tr = el('tr');
    ['平台', '账号', '用户', 'STS 到期', '状态', '更新时间'].forEach(function (t) {
      tr.appendChild(el('th', null, t));
    });
    thead.appendChild(tr);
    table.appendChild(thead);

    var tbody = el('tbody');
    var rows = 0;
    (data.codearts || []).forEach(function (a) {
      var r = el('tr');
      r.appendChild(el('td', null, '码道'));
      var c1 = el('td'); c1.appendChild(el('code', null, a.id)); r.appendChild(c1);
      r.appendChild(el('td', null, a.user || '-'));
      r.appendChild(el('td', null, a.sts_expires || '-'));
      var st = el('td');
      var cls = a.state === 'ok' ? 'ok' : (a.state === 'need_login' || a.state === 'need_relogin') ? 'warn' : 'bad';
      var label = ({ ok: '正常', need_login: '待登录', need_relogin: '需重新登录', unreadable: '记录损坏' })[a.state] || a.state;
      st.className = cls;
      st.textContent = label;
      r.appendChild(st);
      r.appendChild(el('td', null, a.updated_at || '-'));
      tbody.appendChild(r);
      rows++;
    });

    if (!rows) {
      var r = el('tr');
      var td = el('td', 'hint', '还没有码道账号 —— 点右上角「登录 / 管理码道」');
      td.colSpan = 6;
      r.appendChild(td);
      tbody.appendChild(r);
    }
    table.appendChild(tbody);
    box.appendChild(table);
  }

  function renderRequests(d) {
    var box = document.getElementById('aggReqBox');
    if (!box) return;
    var evs = (d && d.events) || [];
    if (!evs.length) {
      box.className = 'hint';
      box.textContent = '还没有请求记录 —— 用 codearts: 开头的模型发一次对话就会出现。';
      return;
    }
    box.className = '';
    var table = el('table', 'table');
    var thead = el('thead');
    var tr = el('tr');
    ['时间', '平台', '模型', '类型', '状态', '耗时', 'token', '错误'].forEach(function (t) {
      tr.appendChild(el('th', null, t));
    });
    thead.appendChild(tr);
    table.appendChild(thead);
    var tbody = el('tbody');
    evs.forEach(function (e) {
      var r = el('tr');
      var t = String(e.time || '').replace('T', ' ').slice(5, 19);
      r.appendChild(el('td', null, t));
      r.appendChild(el('td', null, e.platform || '-'));
      r.appendChild(el('td', null, e.model_raw || e.model || '-'));
      r.appendChild(el('td', null, e.stream ? '流式' : '非流式'));
      var st = el('td', null, String(e.status || ''));
      st.className = e.ok ? 'ok' : 'bad';
      r.appendChild(st);
      r.appendChild(el('td', null, (e.duration_ms || 0) + 'ms'));
      var tok = (e.prompt_tokens || e.output_tokens)
        ? (e.prompt_tokens || 0) + ' / ' + (e.output_tokens || 0) : '-';
      r.appendChild(el('td', null, tok));
      var err = el('td', 'hint', e.err ? String(e.err).slice(0, 80) : '');
      r.appendChild(err);
      tbody.appendChild(r);
    });
    table.appendChild(tbody);
    box.appendChild(table);
  }

  function renderSignin(d) {
    var box = document.getElementById('aggSigninBox');
    if (!box) return;
    var jobs = (d && d.jobs) || [];
    if (!jobs.length) { box.textContent = '还没有签到记录'; return; }
    box.className = '';
    var t = el('table', 'table');
    var th = el('thead'); var tr = el('tr');
    ['任务', '结果', '上次运行', '摘要'].forEach(function (x) { tr.appendChild(el('th', null, x)); });
    th.appendChild(tr); t.appendChild(th);
    var tb = el('tbody');
    var label = { wb: 'WorkBuddy 签到', codearts: '码道签到' };
    jobs.forEach(function (j) {
      var r = el('tr');
      r.appendChild(el('td', null, label[j.name] || j.name));
      var st = el('td', null, j.missing ? '未运行' : (j.ok ? '成功' : '失败(' + j.exit_code + ')'));
      st.className = j.missing ? 'hint' : (j.ok ? 'ok' : 'bad');
      r.appendChild(st);
      r.appendChild(el('td', null, (j.finished_at || '-').replace('T', ' ')));
      r.appendChild(el('td', 'hint', j.summary || ''));
      tb.appendChild(r);
      if (j.log_tail) {
        var tr2 = el('tr');
        var td2 = el('td');
        td2.colSpan = 4;
        var pre = el('pre', 'hint', j.log_tail);
        pre.style.whiteSpace = 'pre-wrap';
        pre.style.fontSize = '11.5px';
        pre.style.maxHeight = '120px';
        pre.style.overflow = 'auto';
        td2.appendChild(pre);
        tr2.appendChild(td2);
        tb.appendChild(tr2);
      }
    });
    t.appendChild(tb);
    box.appendChild(t);
  }

  function renderCredits(data, hist) {
    var box = document.getElementById('aggCreditBox');
    if (!box) return;
    box.className = '';
    box.innerHTML = '';

    var ca = (data.codearts || [])[0];
    if (ca && ca.credits_breakdown) {
      var t = el('table', 'table');
      var th = el('thead'); var tr = el('tr');
      ['口径', '数值'].forEach(function (x) { tr.appendChild(el('th', null, x)); });
      th.appendChild(tr); t.appendChild(th);
      var tb = el('tbody');
      Object.keys(ca.credits_breakdown).forEach(function (k) {
        var r = el('tr');
        r.appendChild(el('td', null, k));
        r.appendChild(el('td', null, String(ca.credits_breakdown[k])));
        tb.appendChild(r);
      });
      t.appendChild(tb);
      box.appendChild(t);

      var sum = el('p', 'hint', '剩余 ' + (ca.credits || 0) + ' / 总额 ' + (ca.credits_total || 0) +
        '　已用 ' + (ca.credits_used || 0) + '　即将过期 ' + (ca.credits_expiring || 0));
      box.appendChild(sum);
    }

    var samples = (hist && hist.samples) || [];
    if (samples.length >= 2) {
      var vals = samples.map(function (s) { return s.credits || 0; });
      var max = Math.max.apply(null, vals), min = Math.min.apply(null, vals);
      var w = 520, hgt = 90, pad = 4;
      var pts = vals.map(function (v, i) {
        var x = pad + i * (w - 2 * pad) / (vals.length - 1);
        var y = hgt - pad - (max === min ? (hgt - 2 * pad) / 2 : (v - min) / (max - min) * (hgt - 2 * pad));
        return x.toFixed(1) + ',' + y.toFixed(1);
      }).join(' ');
      var svgNS = 'http://www.w3.org/2000/svg';
      var svg = document.createElementNS(svgNS, 'svg');
      svg.setAttribute('viewBox', '0 0 ' + w + ' ' + hgt);
      svg.setAttribute('width', '100%');
      svg.setAttribute('height', hgt);
      var poly = document.createElementNS(svgNS, 'polyline');
      poly.setAttribute('points', pts);
      poly.setAttribute('fill', 'none');
      poly.setAttribute('stroke', '#4a9eff');
      poly.setAttribute('stroke-width', '1.6');
      svg.appendChild(poly);
      box.appendChild(svg);
      var note = el('p', 'hint', '最近 ' + samples.length + ' 个采样点，区间 ' + min + ' ~ ' + max +
        '（每 10 分钟一次）');
      box.appendChild(note);
    } else if (ca) {
      box.appendChild(el('p', 'hint', '趋势数据还不够（每 10 分钟采一次），过一会儿再看。'));
    } else {
      box.textContent = '还没有码道账号。';
    }
  }

  function show() {
    var all = document.querySelectorAll('section.view');
    for (var i = 0; i < all.length; i++) all[i].hidden = true;
    var mine = document.getElementById('view-' + VIEW);
    if (mine) mine.hidden = false;
    var links = document.querySelectorAll('nav.nav a[data-view]');
    for (var j = 0; j < links.length; j++) {
      links[j].classList.toggle('on', links[j].getAttribute('data-view') === VIEW);
    }
    api('/panel/agg/api/requests?limit=30').then(renderRequests).catch(function () {});
    api('/panel/agg/api/signin').then(renderSignin).catch(function () {});
    Promise.all([
      api('/panel/agg/api/overview'),
      api('/panel/agg/api/credits/history?hours=72&platform=codearts')
    ]).then(function (r) { renderCredits(r[0], r[1]); }).catch(function () {});
    api('/panel/agg/api/overview').then(render).catch(function (e) {
      var box = document.getElementById('aggPlatformBox');
      if (box) box.textContent = '读取失败：' + e.message + '（可能没填 api_key）';
    });
  }

  function hide() {
    var mine = document.getElementById('view-' + VIEW);
    if (mine) mine.hidden = true;
  }

  function install() {
    var nav = document.querySelector('nav.nav ul');
    if (!nav || document.getElementById('nav-' + VIEW)) return;

    // 导航项（放在「账号池」后面）
    var li = el('li');
    li.id = 'nav-' + VIEW;
    var a = el('a', null, '平台账号');
    a.href = '#' + VIEW;
    a.setAttribute('data-view', VIEW);
    li.appendChild(a);
    var first = nav.querySelector('li');
    if (first && first.nextSibling) nav.insertBefore(li, first.nextSibling);
    else nav.appendChild(li);

    // 把我们的 section 插到第一个 section.view 之前
    var target = document.querySelector('section.view');
    if (target && target.parentNode) target.parentNode.insertBefore(buildSection(), target);
    else document.body.appendChild(buildSection());

    // 只有我们这一项接管点击；其他项由主面板自己处理，我们只负责隐藏自己
    nav.addEventListener('click', function (ev) {
      var link = ev.target.closest ? ev.target.closest('a[data-view]') : null;
      if (!link) return;
      if (link.getAttribute('data-view') === VIEW) {
        ev.preventDefault();
        history.replaceState(null, '', '#' + VIEW);
        show();
      } else {
        hide();
      }
    }, true);

    if (location.hash === '#' + VIEW) show();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', install);
  } else {
    install();
  }



  // ---- 模型与档位页：修正扩展平台模型的能力列文案 ----
  // 码道不提供 supported_efforts，上游模板会渲染成"固定档 · 默认档上游未报"，
  // 看着像坏了。这里只对 codearts: 开头的行改成"平台不提供档位信息"，
  // 不改上游文件（改的是 DOM）。
  function fixModelEffortCells() {
    var body = document.getElementById('mdBody');
    if (!body) return;
    var rows = body.querySelectorAll('tr');
    for (var i = 0; i < rows.length; i++) {
      var r = rows[i];
      if (r.dataset.minisFixed === '1') continue;
      var idEl = r.querySelector('.id');
      var text = (idEl ? idEl.textContent : '') || '';
      if (text.indexOf('codearts:') !== 0) continue;
      var spans = r.querySelectorAll('td span');
      for (var j = 0; j < spans.length; j++) {
        var t = spans[j].textContent || '';
        if (t.indexOf('固定档') >= 0 || t === '不支持思考') {
          spans[j].textContent = '平台不提供档位信息';
          spans[j].title = '码道接口不返回 reasoning effort 列表；请求会按上游默认档走';
        }
      }
      r.dataset.minisFixed = '1';
    }
  }

  function watchModelTable() {
    var body = document.getElementById('mdBody');
    if (!body || body.dataset.minisWatch === '1') return;
    body.dataset.minisWatch = '1';
    var timer = null;
    var mo = new MutationObserver(function () {
      if (timer) clearTimeout(timer);
      timer = setTimeout(fixModelEffortCells, 120);
    });
    mo.observe(body, { childList: true, subtree: true });
    fixModelEffortCells();
  }

  // 面板切到「模型与档位」时表格才建出来，这里挂一个轻量轮询（1 秒一次，开销可忽略）
  setInterval(watchModelTable, 1000);
})();
