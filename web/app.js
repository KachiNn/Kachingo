'use strict';
const $ = (id) => document.getElementById(id);
const STATUSES = [['open', '待办'], ['in_progress', '进行中'], ['done', '已完成']];
const PRIORITY = { low: '低', normal: '中', high: '高' };
const state = { token: localStorage.getItem('kachingo_token') || '', user: null, project: null, ws: null, retry: 0, timer: null };

function el(tag, props, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
    if (k === 'class') node.className = v;
    else if (k.startsWith('on')) node.addEventListener(k.slice(2), v);
    else node.setAttribute(k, v);
  }
  for (const c of children) node.append(c);
  return node;
}

function notify(text, isError) {
  const box = $('message');
  box.textContent = text;
  box.className = isError ? 'error' : '';
  box.hidden = !text;
  if (text && !isError) setTimeout(() => { if (box.textContent === text) box.hidden = true; }, 3000);
}

async function api(path, options = {}) {
  const headers = { Accept: 'application/json' };
  if (options.body !== undefined) headers['Content-Type'] = 'application/json';
  if (state.token) headers.Authorization = 'Bearer ' + state.token;
  const resp = await fetch('/api/v1' + path, { method: options.method || 'GET', headers, body: options.body === undefined ? undefined : JSON.stringify(options.body) });
  if (resp.status === 401 && state.token) { signOut(true); throw new Error('登录已过期，请重新登录'); }
  if (resp.status === 204) return null;
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new Error(data.error || '请求失败（' + resp.status + '）');
  return data;
}

async function guarded(fn) {
  try { await fn(); } catch (err) { notify(err.message, true); }
}

/* ---------- 登录 / 注册 ---------- */
let registerMode = false;
function setMode(register) {
  registerMode = register;
  $('auth-title').textContent = register ? '注册' : '登录';
  $('auth-submit').textContent = register ? '注册并登录' : '登录';
  $('auth-toggle').textContent = register ? '已有账号？登录' : '没有账号？注册';
  $('name-row').hidden = !register;
  $('name').required = register;
  $('password').autocomplete = register ? 'new-password' : 'current-password';
  $('password-hint').hidden = !register;
}

async function authenticate(event) {
  event.preventDefault();
  await guarded(async () => {
    const email = $('email').value.trim();
    const password = $('password').value;
    if (registerMode) {
      const passwordBytes = new TextEncoder().encode(password).length;
      if (passwordBytes < 12 || passwordBytes > 72) {
        throw new Error('注册密码须为 12–72 个 UTF-8 字节');
      }
      await api('/auth/register', { method: 'POST', body: { email, name: $('name').value.trim(), password } });
    }
    const login = await api('/auth/login', { method: 'POST', body: { email, password } });
    state.token = login.token;
    state.user = login.user;
    localStorage.setItem('kachingo_token', state.token);
    $('password').value = '';
    await enterApp();
  });
}

function signOut(expired) {
  if (state.ws) { state.ws.onclose = null; state.ws.close(); state.ws = null; }
  clearTimeout(state.timer);
  state.token = ''; state.user = null; state.project = null;
  localStorage.removeItem('kachingo_token');
  $('app-view').hidden = true; $('user-box').hidden = true; $('auth-view').hidden = false;
  if (!expired) notify('已退出登录');
}

async function logout() {
  try { await api('/auth/logout', { method: 'POST' }); } catch (_) { /* 会话可能已失效 */ }
  signOut(false);
}

async function enterApp() {
  if (!state.user) state.user = await api('/me');
  $('user-name').textContent = state.user.name || state.user.email;
  $('auth-view').hidden = true; $('app-view').hidden = false; $('user-box').hidden = false;
  await loadProjects();
}

/* ---------- 项目 ---------- */
async function loadProjects(selectID) {
  const projects = await api('/projects');
  const list = $('projects');
  list.replaceChildren();
  if (projects.length === 0) list.append(el('li', {}, '还没有项目，先创建一个'));
  for (const p of projects) {
    const item = el('li', { class: state.project && state.project.id === p.id ? 'active' : '', onclick: () => guarded(() => openProject(p)) }, el('span', {}, p.name), el('span', { class: 'badge' }, p.role));
    list.append(item);
  }
  const target = projects.find((p) => p.id === (selectID || (state.project && state.project.id)));
  if (target) await openProject(target);
  else if (!state.project) $('project-view').hidden = true;
}

async function createProject(event) {
  event.preventDefault();
  await guarded(async () => {
    const p = await api('/projects', { method: 'POST', body: { name: $('project-name').value.trim() } });
    $('project-name').value = '';
    await loadProjects(p.id);
  });
}

async function openProject(project) {
  state.project = project;
  $('project-view').hidden = false;
  $('project-title').textContent = project.name;
  $('role-badge').textContent = '我的角色：' + project.role;
  const canWrite = project.role !== 'viewer';
  $('task-form').hidden = !canWrite;
  $('member-form').hidden = project.role !== 'owner';
  for (const item of $('projects').children) item.classList.toggle('active', item.firstChild && item.firstChild.textContent === project.name);
  await Promise.all([loadBoard(), loadMembers()]);
  connectLive();
}

/* ---------- 看板 ---------- */
async function loadBoard() {
  if (!state.project) return;
  const projectID = state.project.id;
  const columns = await api('/projects/' + projectID + '/board?limit=100');
  if (!state.project || state.project.id !== projectID) return;
  const board = $('board');
  board.replaceChildren();
  const today = new Date().toISOString().slice(0, 10);
  columns.forEach((col, index) => {
    const head = el('h3', {}, el('span', {}, STATUSES[index][1]), el('span', { class: 'badge' }, String(col.total)));
    const box = el('div', { class: 'column' }, head);
    for (const task of col.items) box.append(renderTask(task, index, today));
    if (col.total > col.items.length) box.append(el('p', { class: 'meta' }, '仅显示前 ' + col.items.length + ' 条'));
    board.append(box);
  });
}

function renderTask(task, columnIndex, today) {
  const canWrite = state.project.role !== 'viewer';
  const overdue = task.due_date && task.due_date < today && task.status !== 'done';
  const meta = '优先级 ' + (PRIORITY[task.priority] || task.priority) + (task.due_date ? ' · 截止 ' + task.due_date : '') + (overdue ? '（已逾期）' : '');
  const card = el('div', { class: 'task' });
  const comments = el('div', { class: 'comments', hidden: '' });
  card.append(el('div', { class: 'title', onclick: () => guarded(() => toggleComments(task, comments)) }, task.title), el('div', { class: overdue ? 'meta overdue' : 'meta' }, meta));
  if (task.description) card.append(el('div', { class: 'meta' }, task.description));
  if (canWrite) {
    const actions = el('div', { class: 'actions' });
    if (columnIndex > 0) actions.append(el('button', { class: 'small', type: 'button', onclick: () => guarded(() => moveTask(task, STATUSES[columnIndex - 1][0])) }, '← ' + STATUSES[columnIndex - 1][1]));
    if (columnIndex < 2) actions.append(el('button', { class: 'small', type: 'button', onclick: () => guarded(() => moveTask(task, STATUSES[columnIndex + 1][0])) }, STATUSES[columnIndex + 1][1] + ' →'));
    actions.append(el('button', { class: 'small', type: 'button', onclick: () => guarded(() => removeTask(task)) }, '删除'));
    card.append(actions);
  }
  card.append(comments);
  return card;
}

async function createTask(event) {
  event.preventDefault();
  await guarded(async () => {
    const body = { title: $('task-title').value.trim(), priority: $('task-priority').value };
    if ($('task-due').value) body.due_date = $('task-due').value;
    await api('/projects/' + state.project.id + '/tasks', { method: 'POST', body });
    $('task-title').value = ''; $('task-due').value = '';
    await loadBoard();
  });
}

async function moveTask(task, status) {
  try {
    await api('/projects/' + state.project.id + '/tasks/' + task.id, { method: 'PATCH', body: { status, version: task.version } });
  } catch (err) {
    await loadBoard();
    throw err;
  }
  await loadBoard();
}

async function removeTask(task) {
  if (!window.confirm('确定删除任务“' + task.title + '”？评论也会一并删除。')) return;
  await api('/projects/' + state.project.id + '/tasks/' + task.id, { method: 'DELETE' });
  await loadBoard();
}

/* ---------- 评论 ---------- */
async function toggleComments(task, box) {
  if (!box.hidden) { box.hidden = true; return; }
  box.hidden = false;
  await renderComments(task, box);
}

async function renderComments(task, box) {
  const items = await api('/projects/' + state.project.id + '/tasks/' + task.id + '/comments');
  box.replaceChildren();
  if (items.length === 0) box.append(el('p', { class: 'meta' }, '暂无评论'));
  for (const c of items) box.append(el('p', {}, el('strong', {}, (c.author_name || '已注销用户') + '：'), c.body));
  if (state.project.role === 'viewer') return;
  const input = el('input', { type: 'text', placeholder: '写评论…', maxlength: '5000', required: '' });
  const form = el('form', { onsubmit: (event) => { event.preventDefault(); guarded(async () => {
    await api('/projects/' + state.project.id + '/tasks/' + task.id + '/comments', { method: 'POST', body: { body: input.value.trim() } });
    await renderComments(task, box);
  }); } }, input, el('button', { class: 'small', type: 'submit' }, '发送'));
  box.append(form);
}

/* ---------- 成员 ---------- */
async function loadMembers() {
  const members = await api('/projects/' + state.project.id + '/members');
  const list = $('members');
  list.replaceChildren();
  for (const m of members) {
    const row = el('li', {}, el('span', {}, m.user.name + '（' + m.user.email + '）'), el('span', { class: 'badge' }, m.role));
    if (state.project.role === 'owner' && m.role !== 'owner') {
      row.append(el('button', { class: 'small', type: 'button', onclick: () => guarded(async () => {
        if (!window.confirm('移除成员 ' + m.user.name + '？')) return;
        await api('/projects/' + state.project.id + '/members/' + m.user.id, { method: 'DELETE' });
        await loadMembers();
      }) }, '移除'));
    }
    list.append(row);
  }
}

async function addMember(event) {
  event.preventDefault();
  await guarded(async () => {
    await api('/projects/' + state.project.id + '/members', { method: 'POST', body: { email: $('member-email').value.trim(), role: $('member-role').value } });
    $('member-email').value = '';
    await loadMembers();
    notify('已添加成员');
  });
}

/* ---------- 实时刷新 ---------- */
function setLive(on) {
  const badge = $('live');
  badge.textContent = on ? '实时同步中' : '离线';
  badge.classList.toggle('on', on);
}

function connectLive() {
  if (state.ws || !state.token) return;
  const scheme = location.protocol === 'https:' ? 'wss:' : 'ws:';
  const ws = new WebSocket(scheme + '//' + location.host + '/ws?token=' + encodeURIComponent(state.token));
  state.ws = ws;
  let pending = 0;
  ws.onopen = () => { state.retry = 0; setLive(true); };
  ws.onmessage = (event) => {
    let msg;
    try { msg = JSON.parse(event.data); } catch (_) { return; }
    if (!state.project || msg.project_id !== state.project.id) return;
    if (msg.type === 'comment_created') return;
    clearTimeout(pending);
    pending = setTimeout(() => guarded(loadBoard), 200);
  };
  ws.onclose = () => {
    state.ws = null;
    setLive(false);
    if (!state.token) return;
    state.retry = Math.min(state.retry + 1, 6);
    state.timer = setTimeout(connectLive, 1000 * 2 ** state.retry);
  };
}

/* ---------- 启动 ---------- */
$('auth-form').addEventListener('submit', authenticate);
$('auth-toggle').addEventListener('click', () => setMode(!registerMode));
$('logout').addEventListener('click', logout);
$('project-form').addEventListener('submit', createProject);
$('task-form').addEventListener('submit', createTask);
$('member-form').addEventListener('submit', addMember);
if (state.token) guarded(enterApp);
