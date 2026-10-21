const elements = {
  assignments: document.querySelector('#assignments'),
  count: document.querySelector('#count'),
  refresh: document.querySelector('#refresh'),
  status: document.querySelector('#status'),
  statusDot: document.querySelector('#status-dot'),
  template: document.querySelector('#assignment-template'),
  updated: document.querySelector('#updated'),
};

const shortPath = (path) => {
  const clean = path.replace(/\/$/, '').replace(/\/\.git$/, '');
  return clean.split('/').filter(Boolean).at(-1) || clean;
};

const relativeTime = (value) => {
  const seconds = Math.max(0, Math.round((Date.now() - new Date(value).getTime()) / 1000));
  if (seconds < 5) return 'ahora';
  if (seconds < 60) return `hace ${seconds} s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `hace ${minutes} min`;
  return new Intl.DateTimeFormat('es', { dateStyle: 'short', timeStyle: 'short' }).format(new Date(value));
};

const render = (assignments) => {
  elements.assignments.replaceChildren();
  elements.count.textContent = assignments.length;

  if (assignments.length === 0) {
    const empty = document.createElement('div');
    empty.className = 'empty';
    empty.innerHTML = '<strong>No hay bases activas</strong>Las bases aparecerán cuando un agente ejecute acquire desde un worktree.';
    elements.assignments.append(empty);
    return;
  }

  for (const assignment of assignments) {
    const card = elements.template.content.cloneNode(true);
    const article = card.querySelector('article');
    article.querySelector('.database').textContent = assignment.database_name;
    article.querySelector('.template').textContent = `template: ${assignment.template}`;
    article.querySelector('.repository').textContent = shortPath(assignment.repository);
    article.querySelector('.repository').title = assignment.repository;
    article.querySelector('.worktree').textContent = shortPath(assignment.worktree);
    article.querySelector('.worktree').title = assignment.worktree;
    if (assignment.redis_key_prefix) {
      article.querySelector('.redis').textContent = assignment.redis_key_prefix;
      article.querySelector('.redis-row').hidden = false;
    }
    article.querySelector('.heartbeat').textContent = relativeTime(assignment.last_heartbeat);
    article.querySelector('.created').textContent = new Intl.DateTimeFormat('es', { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(assignment.created_at));
    elements.assignments.append(card);
  }
};

async function loadAssignments() {
  elements.refresh.disabled = true;
  elements.assignments.setAttribute('aria-busy', 'true');
  try {
    const response = await fetch('/v1/assignments', { headers: { Accept: 'application/json' } });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const data = await response.json();
    render(data.assignments ?? []);
    elements.status.textContent = 'Broker conectado';
    elements.statusDot.className = 'status-dot online';
    elements.updated.textContent = `Actualizado ${new Intl.DateTimeFormat('es', { timeStyle: 'medium' }).format(new Date())}`;
  } catch (error) {
    elements.status.textContent = 'Sin conexión';
    elements.statusDot.className = 'status-dot error';
    elements.assignments.innerHTML = `<div class="error-panel"><strong>No se pudo consultar el broker</strong>${error.message}</div>`;
  } finally {
    elements.refresh.disabled = false;
    elements.assignments.setAttribute('aria-busy', 'false');
  }
}

elements.refresh.addEventListener('click', loadAssignments);
loadAssignments();
setInterval(loadAssignments, 5000);
