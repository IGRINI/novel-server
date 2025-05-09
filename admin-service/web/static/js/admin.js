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
}); 