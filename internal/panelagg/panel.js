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

  function show() {
    var all = document.querySelectorAll('section.view');
    for (var i = 0; i < all.length; i++) all[i].hidden = true;
    var mine = document.getElementById('view-' + VIEW);
    if (mine) mine.hidden = false;
    var links = document.querySelectorAll('nav.nav a[data-view]');
    for (var j = 0; j < links.length; j++) {
      links[j].classList.toggle('on', links[j].getAttribute('data-view') === VIEW);
    }
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
})();
