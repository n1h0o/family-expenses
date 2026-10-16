const categories = [
  'еда',
  'транспорт',
  'жильё',
  'развлечения',
  'здоровье',
  'покупки',
  'другое',
];

const categoryFilter = document.querySelector('#categoryFilter');
const formCategory = document.querySelector('#formCategory');

for (const category of categories) {
  const label = category[0].toUpperCase() + category.slice(1);
  categoryFilter.add(new Option(label, category));
  formCategory.add(new Option(label, category));
}

const state = {
  page: 1,
  pageSize: 10,
  totalItems: 0,
  totalPages: 1,
  summaryAmount: 0,
  summaryCount: 0,
  items: [],
};

const $ = (selector) => document.querySelector(selector);
const rubles = new Intl.NumberFormat('ru-RU', {
  style: 'currency',
  currency: 'RUB',
  maximumFractionDigits: 2,
});

// экранирует значения из базы перед вставкой в таблицу.
function escapeHTML(value) {
  const replacements = {
    '&': '&amp;',
    '<': '&lt;',
    '>': '&gt;',
    '"': '&quot;',
    "'": '&#39;',
  };

  return String(value ?? '').replace(/[&<>"']/g, (character) => replacements[character]);
}

// собирает параметры списка из выбранных фильтров.
function buildQuery() {
  const params = new URLSearchParams({
    page: String(state.page),
    pageSize: String(state.pageSize),
  });
  const filters = [
    ['userId', '#userFilter'],
    ['from', '#fromFilter'],
    ['to', '#toFilter'],
    ['category', '#categoryFilter'],
  ];

  for (const [key, selector] of filters) {
    const value = $(selector).value;
    if (value) params.set(key, value);
  }

  return params;
}

async function api(path, options = {}) {
  const response = await fetch(path, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      ...(options.headers || {}),
    },
  });

  if (!response.ok) {
    let message = 'Не удалось выполнить запрос';

    try {
      const body = await response.json();
      message = body.error?.message || message;
    } catch {
      // оставляет стандартное сообщение, если сервер вернул не JSON.
    }

    throw new Error(message);
  }

  return response.status === 204 ? null : response.json();
}

function formatDate(date) {
  return new Intl.DateTimeFormat('ru-RU', {
    day: 'numeric',
    month: 'short',
    year: 'numeric',
  }).format(new Date(`${date}T12:00:00`));
}

function renderExpenses() {
  const rows = $('#expenseRows');

  if (state.items.length === 0) {
    rows.innerHTML = `
      <tr>
        <td colspan="6" class="empty">
          Здесь пока нет трат. Добавьте первую запись.
        </td>
      </tr>`;
  } else {
    rows.innerHTML = state.items
      .map((expense) => `
        <tr>
          <td>${escapeHTML(expense.comment || 'Без комментария')}</td>
          <td>${escapeHTML(expense.userId)}</td>
          <td>${escapeHTML(formatDate(expense.spentAt))}</td>
          <td><span class="category-tag">${escapeHTML(expense.category)}</span></td>
          <td>${escapeHTML(rubles.format(expense.amount))}</td>
          <td class="row-actions">
            <button type="button" data-edit="${expense.id}">Изменить</button>
            <button type="button" data-delete="${expense.id}">Удалить</button>
          </td>
        </tr>`)
      .join('');
  }

  $('#totalAmount').textContent = rubles.format(state.summaryAmount);
  $('#expenseCount').textContent = state.summaryCount;
  $('#totalCaption').textContent = 'За весь период с текущими фильтрами';
  $('#resultSummary').textContent = `Показано ${state.items.length} из ${state.totalItems}`;
  $('#pageLabel').textContent = `${state.page} / ${Math.max(1, state.totalPages)}`;
  $('#prevPage').disabled = state.page <= 1;
  $('#nextPage').disabled = state.page >= state.totalPages;
}

// загружает страницу расходов и агрегаты по текущим фильтрам.
async function loadExpenses() {
  const rows = $('#expenseRows');
  rows.innerHTML = '<tr><td colspan="6" class="empty">Загружаем…</td></tr>';

  try {
    const result = await api(`/api/expenses?${buildQuery()}`);
    state.items = result.data;
    state.totalItems = result.pagination.totalItems;
    state.totalPages = result.pagination.totalPages;
    state.summaryAmount = result.summary.amount;
    state.summaryCount = result.summary.count;
    renderExpenses();
  } catch (error) {
    rows.innerHTML = `
      <tr>
        <td colspan="6" class="empty">${escapeHTML(error.message)}</td>
      </tr>`;
  }
}

async function loadMembers() {
  try {
    const result = await api('/api/members');
    const select = $('#userFilter');

    for (const id of result.data) {
      select.add(new Option(`Участник ${id}`, String(id)));
    }
  } catch {
    // список участников останется доступен после появления первой записи.
  }
}

function showToast(message) {
  const toast = $('#toast');
  toast.textContent = message;
  toast.classList.add('show');
  setTimeout(() => toast.classList.remove('show'), 2400);
}

function openExpenseForm(expense) {
  $('#expenseForm').reset();
  $('#formError').textContent = '';
  $('#expenseId').value = expense?.id || '';
  $('#dialogKicker').textContent = expense ? 'Редактирование' : 'Новая запись';
  $('#dialogTitle').textContent = expense ? 'Изменить трату' : 'Добавить трату';
  $('#saveExpense').textContent = expense ? 'Сохранить изменения' : 'Сохранить';
  $('#formUserId').value = expense?.userId || '';
  $('#formAmount').value = expense?.amount || '';
  $('#formDate').value = expense?.spentAt || new Date().toISOString().slice(0, 10);
  $('#formCategory').value = expense?.category || 'другое';
  $('#formComment').value = expense?.comment || '';
  $('#expenseDialog').showModal();
}

function closeExpenseForm() {
  $('#expenseDialog').close();
}

$('#addButton').addEventListener('click', () => openExpenseForm());
$('#closeDialog').addEventListener('click', closeExpenseForm);
$('#cancelDialog').addEventListener('click', closeExpenseForm);

$('#expenseRows').addEventListener('click', async (event) => {
  const editButton = event.target.closest('[data-edit]');
  const deleteButton = event.target.closest('[data-delete]');

  if (editButton) {
    const expense = state.items.find((item) => item.id === Number(editButton.dataset.edit));
    if (expense) openExpenseForm(expense);
  }

  if (deleteButton) {
    try {
      await api(`/api/expenses/${deleteButton.dataset.delete}`, { method: 'DELETE' });
      showToast('Трата удалена');
      await loadExpenses();
    } catch (error) {
      showToast(error.message);
    }
  }
});

$('#expenseForm').addEventListener('submit', async (event) => {
  event.preventDefault();

  const id = $('#expenseId').value;
  const payload = {
    userId: Number($('#formUserId').value),
    amount: Number($('#formAmount').value),
    spentAt: $('#formDate').value,
    category: $('#formCategory').value,
    comment: $('#formComment').value.trim(),
  };

  $('#saveExpense').disabled = true;
  $('#formError').textContent = '';

  try {
    await api(id ? `/api/expenses/${id}` : '/api/expenses', {
      method: id ? 'PATCH' : 'POST',
      body: JSON.stringify(payload),
    });
    closeExpenseForm();
    showToast(id ? 'Изменения сохранены' : 'Трата добавлена');
    state.page = 1;
    await loadExpenses();
    await loadMembers();
  } catch (error) {
    $('#formError').textContent = error.message;
  } finally {
    $('#saveExpense').disabled = false;
  }
});

for (const selector of ['#userFilter', '#fromFilter', '#toFilter', '#categoryFilter']) {
  $(selector).addEventListener('change', () => {
    state.page = 1;
    loadExpenses();
  });
}

$('#resetFilters').addEventListener('click', () => {
  for (const selector of ['#userFilter', '#fromFilter', '#toFilter', '#categoryFilter']) {
    $(selector).value = '';
  }

  state.page = 1;
  loadExpenses();
});

$('#prevPage').addEventListener('click', () => {
  if (state.page > 1) {
    state.page -= 1;
    loadExpenses();
  }
});

$('#nextPage').addEventListener('click', () => {
  if (state.page < state.totalPages) {
    state.page += 1;
    loadExpenses();
  }
});

const now = new Date();
const monthStart = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-01`;
$('#fromFilter').value = monthStart;
$('#todayLabel').textContent = new Intl.DateTimeFormat('ru-RU', {
  weekday: 'long',
  day: 'numeric',
  month: 'long',
}).format(now);

loadMembers().then(loadExpenses);
