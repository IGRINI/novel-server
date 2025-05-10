// Общий JS для админ-панели

document.addEventListener('DOMContentLoaded', function() {
    // Отправка уведомления пользователю
    const sendButton = document.getElementById('sendNotificationBtn');
    if (sendButton) {
        sendButton.addEventListener('click', function() {
            const userId = this.getAttribute('data-user-id');
            const titleElem = document.getElementById('notificationTitle');
            const messageElem = document.getElementById('notificationMessage');
            const statusDiv = document.getElementById('notificationStatus');

            const title = titleElem.value.trim();
            const message = messageElem.value.trim();

            if (!title) {
                statusDiv.textContent = 'Введите заголовок уведомления.';
                statusDiv.className = 'mt-2 text-danger';
                return;
            }
            if (!message) {
                statusDiv.textContent = 'Введите текст уведомления.';
                statusDiv.className = 'mt-2 text-danger';
                return;
            }

            statusDiv.textContent = 'Отправка...';
            statusDiv.className = 'mt-2 text-info';
            sendButton.disabled = true;
            sendButton.style.opacity = '0.7';

            fetch(`/admin/users/${userId}/send-notification`, {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify({ title: title, message: message })
            })
            .then(response => {
                if (!response.ok) {
                    return response.json().then(err => { throw new Error(err.error || `Ошибка ${response.status}`) });
                }
                return response.json();
            })
            .then(data => {
                statusDiv.textContent = 'Уведомление успешно отправлено в очередь!';
                statusDiv.className = 'mt-2 text-success';
                titleElem.value = '';
                messageElem.value = '';
            })
            .catch(error => {
                console.error('Ошибка отправки уведомления:', error);
                statusDiv.textContent = `Ошибка: ${error.message || 'Не удалось отправить'}`;
                statusDiv.className = 'mt-2 text-danger';
            })
            .finally(() => {
                sendButton.disabled = false;
                sendButton.style.opacity = '1';
            });
        });
    }

    const themeToggle = document.getElementById('theme-toggle');
    if (themeToggle) {
        const stored = localStorage.getItem('theme');
        const initial = stored || 'light';
        document.documentElement.setAttribute('data-theme', initial);
        themeToggle.textContent = initial === 'light' ? '🌙' : '☀️';
        themeToggle.addEventListener('click', function() {
            const current = document.documentElement.getAttribute('data-theme');
            const next = current === 'light' ? 'dark' : 'light';
            document.documentElement.setAttribute('data-theme', next);
            localStorage.setItem('theme', next);
            themeToggle.textContent = next === 'light' ? '🌙' : '☀️';
        });
    }

    // Фильтрация и пагинация таблиц
    document.querySelectorAll('.data-table').forEach(table => {
        const wrapper = document.createElement('div');
        wrapper.className = 'table-with-filter';
        const input = document.createElement('input');
        input.type = 'text';
        input.className = 'table-filter form-control';
        input.placeholder = 'Поиск...';
        const paginationContainer = document.createElement('div');
        paginationContainer.className = 'table-pagination';

        const rows = Array.from(table.querySelectorAll('tbody tr'));
        let currentPage = 1;
        const rowsPerPage = 10;

        table.parentNode.insertBefore(wrapper, table);
        wrapper.appendChild(input);
        wrapper.appendChild(table);
        wrapper.appendChild(paginationContainer);

        function render() {
            const filter = input.value.toLowerCase();
            const filteredRows = rows.filter(row => row.textContent.toLowerCase().includes(filter));
            const totalPages = Math.max(Math.ceil(filteredRows.length / rowsPerPage), 1);
            if (currentPage > totalPages) currentPage = totalPages;

            rows.forEach(row => row.style.display = 'none');
            filteredRows.forEach((row, idx) => {
                if (idx >= (currentPage - 1) * rowsPerPage && idx < currentPage * rowsPerPage) {
                    row.style.display = '';
                }
            });

            paginationContainer.innerHTML =
                `<button class="prev btn-sm cta-button cta-button--secondary" ${currentPage===1?'disabled':''}>&lt;</button>
                 <span>${currentPage}/${totalPages}</span>
                 <button class="next btn-sm cta-button cta-button--secondary" ${currentPage===totalPages?'disabled':''}>&gt;</button>`;
            const prevBtn = paginationContainer.querySelector('.prev');
            const nextBtn = paginationContainer.querySelector('.next');
            prevBtn.addEventListener('click', () => { if (currentPage > 1) { currentPage--; render(); } });
            nextBtn.addEventListener('click', () => { if (currentPage < totalPages) { currentPage++; render(); } });
        }

        input.addEventListener('input', () => { currentPage = 1; render(); });
        render();
    });
});

// JSON format utilities and scene panel management
// Auto-format JSON textareas on pages with config/setup
if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', () => {
        if (document.getElementById('configJson')) { formatJsonTextarea('configJson'); }
        if (document.getElementById('setupJson')) { formatJsonTextarea('setupJson'); }
    });
} else {
    if (document.getElementById('configJson')) { formatJsonTextarea('configJson'); }
    if (document.getElementById('setupJson')) { formatJsonTextarea('setupJson'); }
}

function formatJsonTextarea(textareaId) {
    const textarea = document.getElementById(textareaId);
    if (textarea && textarea.value.trim()) {
        try {
            const obj = JSON.parse(textarea.value);
            textarea.value = JSON.stringify(obj, null, 2);
        } catch (e) {
            alert(`Некорректный JSON в поле ${textareaId}!`);
        }
    }
}

function formatScenePanelJson() {
    const ta = document.getElementById('scenePanelJsonContentEditable');
    if (!ta) return;
    try {
        const obj = JSON.parse(ta.value);
        ta.value = JSON.stringify(obj, null, 2);
    } catch {
        alert('Некорректный JSON!');
    }
}

function showSceneContent(sceneId) {
    const orig = document.getElementById(`scene-${sceneId}`);
    const panel = document.getElementById('sceneJsonPanel');
    const ta = document.getElementById('scenePanelJsonContentEditable');
    const title = document.getElementById('scenePanelTitle');
    if (!panel || !ta || !title) return;
    ta.value = orig ? orig.value : '';
    title.textContent = sceneId;
    panel.style.display = 'block';
}

function hideScenePanel() {
    const panel = document.getElementById('sceneJsonPanel');
    if (panel) panel.style.display = 'none';
}

function saveScenePanelContent() {
    const pageData = document.getElementById('page-data');
    if (!pageData) return;
    const userId = pageData.dataset.userId;
    const storyId = pageData.dataset.storyId;
    const sceneId = document.getElementById('scenePanelTitle').textContent;
    const contentJson = document.getElementById('scenePanelJsonContentEditable').value;
    try { JSON.parse(contentJson); } catch { alert('Некорректный JSON!'); return; }
    fetch(`/admin/users/${userId}/stories/${storyId}/scenes/${sceneId}`, {
        method: 'POST',
        headers: { 'Content-Type':'application/json','Accept':'application/json' },
        body: JSON.stringify({ contentJson })
    })
    .then(res => {
        if (!res.ok) throw new Error();
        const orig = document.getElementById(`scene-${sceneId}`);
        if (orig) orig.value = contentJson;
        hideScenePanel();
    })
    .catch(() => alert('Ошибка сохранения сцены'));
}

// Функция для копирования текста в буфер обмена
function copyToClipboard(text) {
    if (!navigator.clipboard) {
        alert('Ваш браузер не поддерживает копирование через Clipboard API');
        return;
    }
    navigator.clipboard.writeText(text)
        .then(() => {
            // Показываем системное сообщение об успехе
            const msg = document.createElement('div');
            msg.className = 'system-message system-message--success';
            msg.textContent = `Скопировано: ${text}`;
            document.body.prepend(msg);
            setTimeout(() => msg.remove(), 3000);
        })
        .catch(() => {
            alert('Не удалось скопировать текст');
        });
}

// Функция показа модального окна подтверждения, возвращает Promise<boolean>
function showModalConfirm(message) {
    return new Promise(resolve => {
        const modalEl = document.getElementById('confirmModal');
        const modalBody = document.getElementById('confirmModalBody');
        const okBtn = document.getElementById('confirmModalOk');
        modalBody.textContent = message;
        const bsModal = new bootstrap.Modal(modalEl);
        const onOk = () => { cleanup(); resolve(true); };
        const onHide = () => { cleanup(); resolve(false); };
        function cleanup() {
            okBtn.removeEventListener('click', onOk);
            modalEl.removeEventListener('hidden.bs.modal', onHide);
        }
        okBtn.addEventListener('click', onOk);
        modalEl.addEventListener('hidden.bs.modal', onHide);
        bsModal.show();
    });
}

// Перехват кнопок с hx-delete для использования модального подтверждения вместо native confirm
document.body.addEventListener('click', function(event) {
    const btn = event.target.closest('button[hx-delete]');
    if (btn) {
        event.preventDefault();
        const url = btn.getAttribute('hx-delete');
        const message = btn.getAttribute('hx-confirm') || 'Вы уверены?';
        showModalConfirm(message).then(confirmed => {
            if (confirmed) {
                htmx.ajax('DELETE', url, {
                    target: () => btn.closest('tr'),
                    swap: 'outerHTML'
                });
            }
        });
    }
}); 